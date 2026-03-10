// === SyncWave Client: CRDT Shadow + Collaboration + AI + Offline Sync ===

(function() {
    const editor = document.getElementById("editor");
    const ghostText = document.getElementById("ghost-text");
    const statusDot = document.getElementById("statusDot");
    const statusText = document.getElementById("statusText");
    const avatarsDiv = document.getElementById("avatars");
    const logContainer = document.getElementById("logContainer");
    const charCountEl = document.getElementById("charCount");
    const activityBtn = document.getElementById("activityBtn");
    const activityPanel = document.getElementById("activityPanel");
    const aiBadge = document.getElementById("aiBadge");
    const offlineBanner = document.getElementById("offlineBanner");
    const cursorsOverlay = document.getElementById("cursors-overlay");
    const nameModal = document.getElementById("nameModal");
    const nameInput = document.getElementById("nameInput");
    const nameSubmit = document.getElementById("nameSubmit");

    // --- Identity ---
    const userID = "U-" + Math.random().toString(36).substring(2, 6).toUpperCase();
    let userName = "Anonymous";
    let myColor = "#7a5cff";
    let isRemoteUpdate = false;
    let started = false;

    // --- Name Modal ---
    function submitName() {
        var val = nameInput.value.trim();
        if (val) userName = val;
        nameModal.classList.add("hidden");
        editor.focus();
        if (!started) { started = true; initApp(); }
    }
    nameSubmit.addEventListener("click", submitName);
    nameInput.addEventListener("keydown", function(e) {
        if (e.key === "Enter") submitName();
    });

    // --- Connection State ---
    let ws = null;
    let connected = false;
    let lastSeq = 0;
    let offlineBuffer = [];
    let reconnectAttempts = 0;
    let reconnectTimer = null;
    // The editor content at the moment of last disconnect — used as the "base"
    // for three-way merge when reconnecting with offline edits.
    let lastSyncedContent = null;
    let wasDisconnected = false;

    // --- Local CRDT Shadow ---
    // Ordered array of { id: {clock, siteID}, char: string }
    // Mirrors the server's visible document state for position<->OpID mapping.
    let shadow = [];
    let placeholderCounter = 0; // Monotonic counter for unique placeholder IDs
    const ROOT_ID = { clock: 0, siteID: "ROOT" };

    // Three-way merge: given a common base, our local text, and the server text,
    // produce a merged result that keeps BOTH sets of changes.
    function mergeTexts(base, local, server) {
        // If no base (first connect), just accept the server
        if (!base) return server;
        // If we didn't change anything offline, accept server
        if (base === local) return server;
        // If server already contains our text (we were the restorer), nothing to merge
        if (local === server) return server;

        // Find what WE changed relative to the base
        var prefix = 0;
        var min1 = Math.min(base.length, local.length);
        while (prefix < min1 && base.charAt(prefix) === local.charAt(prefix)) prefix++;

        var suffix = 0;
        var maxSuffix1 = Math.min(base.length - prefix, local.length - prefix);
        while (suffix < maxSuffix1 &&
               base.charAt(base.length - 1 - suffix) === local.charAt(local.length - 1 - suffix)) {
            suffix++;
        }

        var ourInserted = local.substring(prefix, local.length - suffix);
        // If nothing was inserted, accept server as-is
        if (ourInserted.length === 0) return server;

        // Find common regions between base and server to map our insertion point
        var sharedPrefix = 0;
        var min2 = Math.min(base.length, server.length);
        while (sharedPrefix < min2 && base.charAt(sharedPrefix) === server.charAt(sharedPrefix)) {
            sharedPrefix++;
        }

        var sharedSuffix = 0;
        var maxSuffix2 = Math.min(base.length - sharedPrefix, server.length - sharedPrefix);
        while (sharedSuffix < maxSuffix2 &&
               base.charAt(base.length - 1 - sharedSuffix) === server.charAt(server.length - 1 - sharedSuffix)) {
            sharedSuffix++;
        }

        // Map our insertion position (in base coordinates) to server coordinates.
        // Base has 3 regions: [0,sharedPrefix) [sharedPrefix, base.len-sharedSuffix) [base.len-sharedSuffix, base.len)
        // Server:            [0,sharedPrefix) [sharedPrefix, server.len-sharedSuffix) [server.len-sharedSuffix, server.len)
        var insertPos;
        if (prefix < sharedPrefix) {
            // Our change is in the shared prefix → same position in server
            insertPos = prefix;
        } else if (prefix >= base.length - sharedSuffix) {
            // Our change is in the shared suffix → offset into server's suffix
            insertPos = (server.length - sharedSuffix) + (prefix - (base.length - sharedSuffix));
        } else {
            // Our change is in the divergent region → place after server's changes
            insertPos = server.length - sharedSuffix;
        }

        return server.substring(0, insertPos) + ourInserted + server.substring(insertPos);
    }

    function shadowIDAtPos(pos) {
        if (pos < 0 || pos >= shadow.length) return ROOT_ID;
        return shadow[pos].id;
    }

    function shadowAnchorForPos(pos) {
        if (pos <= 0) return ROOT_ID;
        return shadowIDAtPos(pos - 1);
    }

    function shadowInsert(pos, char, id) {
        shadow.splice(pos, 0, { id: id, char: char });
    }

    function shadowDelete(pos) {
        if (pos >= 0 && pos < shadow.length) {
            shadow.splice(pos, 1);
        }
    }

    function shadowFindPos(id) {
        for (var i = 0; i < shadow.length; i++) {
            if (shadow[i].id.clock === id.clock && shadow[i].id.siteID === id.siteID) {
                return i;
            }
        }
        return -1;
    }

    function rebuildShadow(content, nodeIDs) {
        shadow = [];
        if (nodeIDs && nodeIDs.length > 0) {
            for (var i = 0; i < nodeIDs.length; i++) {
                shadow.push({
                    id: nodeIDs[i],
                    char: content.charAt(i) || ""
                });
            }
        } else {
            // Fallback: no nodeIDs, build with placeholder IDs
            for (var j = 0; j < content.length; j++) {
                shadow.push({
                    id: { clock: -(j + 1), siteID: "UNKNOWN" },
                    char: content.charAt(j)
                });
            }
        }
    }

    // --- AI State ---
    var AI_DEBOUNCE_MS = 800;
    var AI_MIN_LENGTH = 10;
    let aiDebounceTimer = null;
    let currentSuggestion = "";
    let suggestionCursorPos = -1; // Cursor position when suggestion was requested

    // --- Remote Cursors ---
    let remoteCursors = {}; // userID -> { position, userName, color }

    // --- Old Value Tracking for Input Diffing ---
    let oldValue = "";

    // --- Activity Panel ---
    activityBtn.addEventListener("click", function() {
        activityPanel.classList.toggle("open");
    });

    // ==========================================
    //   WEBSOCKET CONNECTION
    // ==========================================
    function connect() {
        try {
            var wsProtocol = location.protocol === "https:" ? "wss://" : "ws://";
            ws = new WebSocket(wsProtocol + location.host + "/ws");
        } catch(e) {
            scheduleReconnect();
            return;
        }

        ws.onopen = function() {
            connected = true;
            reconnectAttempts = 0;
            setConnectionStatus("online");

            // Only send the join. Offline batch_sync is deferred until AFTER we receive
            // the server's full_sync — otherwise we race and the initial full_sync can
            // overwrite the merged result.
            ws.send(JSON.stringify({
                type: "join",
                userID: userID,
                userName: userName,
                lastSeq: lastSeq
            }));
        };

        ws.onclose = function() {
            // Only snapshot on the FIRST disconnect (connected→disconnected),
            // not on retry failures. Otherwise retry onclose events overwrite
            // lastSyncedContent with the current (offline-edited) text, making
            // the three-way merge think nothing changed.
            if (connected) {
                lastSyncedContent = editor.value;
            }
            connected = false;
            wasDisconnected = true;
            pendingRestore = false; // Reset in case we were waiting
            setConnectionStatus("offline");
            addLog("System", "Connection lost — edits saved locally");
            scheduleReconnect();
        };

        ws.onerror = function() {};

        ws.onmessage = function(event) {
            var msg;
            try {
                msg = JSON.parse(event.data);
            } catch(e) {
                return;
            }

            if (msg.seq) lastSeq = msg.seq;

            switch (msg.type) {
                case "full_sync":
                    isRemoteUpdate = true;
                    var savedCursor = editor.selectionStart;
                    var serverContent = (msg.content != null) ? msg.content : "";
                    var localText = editor.value;
                    myColor = msg.color || myColor;

                    // ---- RESTORE RESPONSE ----
                    // If we sent a 'restore' message, this full_sync is the server's
                    // response (now with real content). Do a normal merge against it.
                    if (pendingRestore) {
                        pendingRestore = false;
                        var merged = mergeTexts(lastSyncedContent, localText, serverContent);
                        rebuildShadow(serverContent, msg.nodeIDs || []);
                        offlineBuffer = [];

                        if (merged !== serverContent) {
                            var freshOps = diffToOps(serverContent, merged, merged.length);
                            if (freshOps.length > 0) {
                                editor.value = merged;
                                oldValue = merged;
                                addLog("System", "Merging offline additions...");
                                ws.send(JSON.stringify({
                                    type: "batch_sync",
                                    content: JSON.stringify(freshOps)
                                }));
                            } else {
                                editor.value = serverContent;
                                oldValue = serverContent;
                            }
                        } else {
                            editor.value = serverContent;
                            oldValue = serverContent;
                        }

                        wasDisconnected = false;
                        lastSyncedContent = editor.value;
                        editor.selectionStart = editor.selectionEnd = Math.min(savedCursor, editor.value.length);
                        updateCharCount();
                        addLog("System", "Document restored (" + editor.value.length + " chars)");
                        isRemoteUpdate = false;
                        break;
                    }

                    // ---- CHECK FOR OFFLINE EDITS ----
                    var hasOfflineEdits = wasDisconnected &&
                        lastSyncedContent !== null &&
                        localText !== lastSyncedContent &&
                        localText.length > 0;

                    // ---- SERVER RESTART (empty doc) ----
                    // Send a 'restore' message so the server gets ONE authoritative copy.
                    // Other tabs that also see empty will get the restored content when
                    // their 'restore' response arrives (first restorer wins on server).
                    if (hasOfflineEdits && serverContent.length === 0) {
                        pendingRestore = true;
                        ws.send(JSON.stringify({
                            type: "restore",
                            content: localText
                        }));
                        addLog("System", "Restoring document to server...");
                        isRemoteUpdate = false;
                        break;
                    }

                    // ---- NORMAL MERGE ----
                    if (hasOfflineEdits) {
                        var merged = mergeTexts(lastSyncedContent, localText, serverContent);
                        rebuildShadow(serverContent, msg.nodeIDs || []);
                        offlineBuffer = [];
                        var freshOps = diffToOps(serverContent, merged, merged.length);

                        if (freshOps.length > 0) {
                            editor.value = merged;
                            oldValue = merged;
                            addLog("System", "Merging " + freshOps.length + " offline changes...");
                            ws.send(JSON.stringify({
                                type: "batch_sync",
                                content: JSON.stringify(freshOps)
                            }));
                        } else {
                            editor.value = serverContent;
                            oldValue = serverContent;
                        }
                    } else {
                        // Normal sync — accept server state
                        editor.value = serverContent;
                        rebuildShadow(serverContent, msg.nodeIDs || []);
                        oldValue = serverContent;
                        offlineBuffer = [];
                    }

                    wasDisconnected = false;
                    lastSyncedContent = editor.value;
                    editor.selectionStart = editor.selectionEnd = Math.min(savedCursor, editor.value.length);
                    updateCharCount();
                    addLog("System", "Document synced (" + editor.value.length + " chars)");
                    isRemoteUpdate = false;
                    break;

                case "insert":
                    // During pending restore, ignore remote ops — the restore's
                    // full_sync response will have the complete state.
                    if (pendingRestore) break;
                    if (msg.userID === userID) {
                        // Our own op confirmed by server — update shadow with server-assigned ID.
                        // Ops are confirmed in FIFO order, so the first placeholder (clock < 0)
                        // in the shadow is always the correct match.
                        if (msg.newID) {
                            for (var pi = 0; pi < shadow.length; pi++) {
                                if (shadow[pi].id.clock < 0) {
                                    shadow[pi].id = msg.newID;
                                    break;
                                }
                            }
                        }
                        break;
                    }
                    isRemoteUpdate = true;
                    var insPos = msg.position;
                    if (insPos === undefined || insPos === null) insPos = 0;
                    var insCh = String.fromCharCode(msg.char);
                    var ss = editor.selectionStart;
                    var se = editor.selectionEnd;

                    // Insert into shadow
                    if (msg.newID) {
                        shadowInsert(insPos, insCh, msg.newID);
                    }

                    // Insert into editor
                    var before = editor.value.substring(0, insPos);
                    var after = editor.value.substring(insPos);
                    editor.value = before + insCh + after;
                    editor.selectionStart = (insPos <= ss) ? ss + 1 : ss;
                    editor.selectionEnd = (insPos <= se) ? se + 1 : se;

                    oldValue = editor.value;
                    updateCharCount();
                    clearSuggestion();
                    isRemoteUpdate = false;
                    break;

                case "delete":
                    if (pendingRestore) break;
                    if (msg.userID === userID) {
                        // Our own delete confirmed — shadow was already updated in diffToOps.
                        break;
                    }
                    isRemoteUpdate = true;
                    var dpos = (msg.position != null) ? msg.position : -1;
                    if (dpos >= 0 && dpos < editor.value.length) {
                        var dss = editor.selectionStart;
                        var dse = editor.selectionEnd;

                        // Delete from shadow
                        shadowDelete(dpos);

                        // Delete from editor
                        var dbefore = editor.value.substring(0, dpos);
                        var dafter = editor.value.substring(dpos + 1);
                        editor.value = dbefore + dafter;
                        editor.selectionStart = (dpos < dss) ? dss - 1 : dss;
                        editor.selectionEnd = (dpos < dse) ? dse - 1 : dse;
                    }
                    oldValue = editor.value;
                    updateCharCount();
                    clearSuggestion();
                    isRemoteUpdate = false;
                    break;

                case "cursor":
                    if (msg.userID !== userID) {
                        remoteCursors[msg.userID] = {
                            position: msg.position,
                            userName: msg.userName,
                            color: msg.color
                        };
                        renderRemoteCursors();
                    }
                    break;

                case "presence":
                    renderUsers(msg.users || []);
                    // Clean up cursors for users who left
                    if (msg.users) {
                        var activeIDs = {};
                        for (var i = 0; i < msg.users.length; i++) {
                            activeIDs[msg.users[i].id] = true;
                        }
                        for (var uid in remoteCursors) {
                            if (!activeIDs[uid]) delete remoteCursors[uid];
                        }
                        renderRemoteCursors();
                    }
                    break;
            }
        };
    }

    function scheduleReconnect() {
        if (reconnectTimer) clearTimeout(reconnectTimer);
        reconnectAttempts++;
        var delay = Math.min(1000 * Math.pow(2, reconnectAttempts - 1), 30000);
        addLog("System", "Reconnecting in " + (delay / 1000) + "s...");
        reconnectTimer = setTimeout(function() { connect(); }, delay);
    }

    function setConnectionStatus(status) {
        if (status === "online") {
            statusDot.classList.add("on");
            statusText.textContent = "Connected";
            if (offlineBanner) offlineBanner.style.display = "none";
        } else {
            statusDot.classList.remove("on");
            statusText.textContent = "Offline";
            if (offlineBanner) offlineBanner.style.display = "flex";
        }
    }

    function sendOp(op) {
        if (connected && ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify(op));
        } else {
            offlineBuffer.push(op);
            updateCharCount();
        }
    }

    // ==========================================
    //   LOCAL EDITING — Input Diffing
    // ==========================================
    // Capture old value before any input event
    editor.addEventListener("beforeinput", function() {
        oldValue = editor.value;
    });

    editor.addEventListener("input", function(e) {
        if (isRemoteUpdate) return;

        clearSuggestion();

        var newValue = editor.value;
        var ops = diffToOps(oldValue, newValue, editor.selectionStart);

        for (var i = 0; i < ops.length; i++) {
            sendOp(ops[i]);
        }

        oldValue = newValue;
        updateCharCount();
        scheduleAICompletion();
    });

    // Diff old value vs new value and produce insert/delete ops with anchorID/targetID.
    function diffToOps(oldVal, newVal, cursorAfter) {
        var ops = [];

        // Find common prefix
        var prefixLen = 0;
        var minLen = Math.min(oldVal.length, newVal.length);
        while (prefixLen < minLen && oldVal.charAt(prefixLen) === newVal.charAt(prefixLen)) {
            prefixLen++;
        }

        // Find common suffix (from end, not overlapping prefix)
        var suffixLen = 0;
        while (
            suffixLen < (oldVal.length - prefixLen) &&
            suffixLen < (newVal.length - prefixLen) &&
            oldVal.charAt(oldVal.length - 1 - suffixLen) === newVal.charAt(newVal.length - 1 - suffixLen)
        ) {
            suffixLen++;
        }

        var deletedCount = oldVal.length - prefixLen - suffixLen;
        var insertedStr = newVal.substring(prefixLen, newVal.length - suffixLen);

        // Generate delete ops (in reverse order so positions stay valid)
        for (var d = deletedCount - 1; d >= 0; d--) {
            var delPos = prefixLen + d;
            var targetID = shadowIDAtPos(delPos);
            ops.push({
                type: "delete",
                position: delPos,
                targetID: targetID
            });
        }

        // Apply deletes to shadow immediately
        for (var dd = deletedCount - 1; dd >= 0; dd--) {
            shadowDelete(prefixLen + dd);
        }

        // Generate insert ops
        for (var ins = 0; ins < insertedStr.length; ins++) {
            var insPos = prefixLen + ins;
            var anchorID = shadowAnchorForPos(insPos);
            placeholderCounter++;
            var placeholderID = { clock: -placeholderCounter, siteID: userID };
            var ch = insertedStr.charAt(ins);

            ops.push({
                type: "insert",
                char: ch.charCodeAt(0),
                position: insPos,
                anchorID: anchorID
            });

            // Insert placeholder into shadow immediately
            shadowInsert(insPos, ch, placeholderID);
        }

        return ops;
    }

    // ==========================================
    //   REMOTE CURSOR RENDERING
    // ==========================================
    function broadcastCursor() {
        if (!connected) return;
        sendOp({
            type: "cursor",
            position: editor.selectionStart
        });
    }

    document.addEventListener("selectionchange", function() {
        if (document.activeElement === editor && !isRemoteUpdate) {
            broadcastCursor();
        }
    });

    function renderRemoteCursors() {
        if (!cursorsOverlay) return;
        cursorsOverlay.innerHTML = "";

        for (var uid in remoteCursors) {
            var cursor = remoteCursors[uid];
            var pos = cursor.position;
            if (pos === undefined || pos === null) continue;

            var coords = getCaretCoordinates(pos);
            if (!coords) continue;

            // Cursor line
            var cursorEl = document.createElement("div");
            cursorEl.className = "remote-cursor";
            cursorEl.style.left = coords.left + "px";
            cursorEl.style.top = coords.top + "px";
            cursorEl.style.height = coords.height + "px";
            cursorEl.style.backgroundColor = cursor.color;

            // Label
            var label = document.createElement("div");
            label.className = "remote-cursor-label";
            label.style.backgroundColor = cursor.color;
            label.textContent = cursor.userName;
            cursorEl.appendChild(label);

            cursorsOverlay.appendChild(cursorEl);
        }
    }

    // Measure caret coordinates by using a mirror div technique
    function getCaretCoordinates(pos) {
        var mirror = document.getElementById("caret-mirror");
        if (!mirror) {
            mirror = document.createElement("div");
            mirror.id = "caret-mirror";
            mirror.style.cssText = "position:absolute;top:0;left:0;visibility:hidden;white-space:pre-wrap;word-wrap:break-word;overflow:hidden;pointer-events:none;";
            document.getElementById("editor-host").appendChild(mirror);
        }

        // Copy editor styles
        var cs = window.getComputedStyle(editor);
        mirror.style.width = cs.width;
        mirror.style.fontFamily = cs.fontFamily;
        mirror.style.fontSize = cs.fontSize;
        mirror.style.lineHeight = cs.lineHeight;
        mirror.style.padding = cs.padding;
        mirror.style.border = cs.border;
        mirror.style.letterSpacing = cs.letterSpacing;
        mirror.style.wordSpacing = cs.wordSpacing;
        mirror.style.tabSize = cs.tabSize;

        var text = editor.value.substring(0, pos);
        var textNode = document.createTextNode(text);
        var span = document.createElement("span");
        span.textContent = "|";

        mirror.innerHTML = "";
        mirror.appendChild(textNode);
        mirror.appendChild(span);

        var editorRect = editor.getBoundingClientRect();
        var hostRect = document.getElementById("editor-host").getBoundingClientRect();
        var spanRect = span.getBoundingClientRect();

        return {
            left: spanRect.left - hostRect.left,
            top: spanRect.top - hostRect.top - editor.scrollTop,
            height: parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.8
        };
    }

    // ==========================================
    //   AI: Ghost Text Suggestions
    // ==========================================
    function scheduleAICompletion() {
        clearTimeout(aiDebounceTimer);
        if (!connected) return;
        var text = editor.value;
        if (text.length < AI_MIN_LENGTH) return;
        aiDebounceTimer = setTimeout(function() {
            if (shouldRequestAI()) requestAICompletion();
        }, AI_DEBOUNCE_MS);
    }

    function shouldRequestAI() {
        var text = editor.value;
        var pos = editor.selectionStart;
        if (text.length < AI_MIN_LENGTH) return false;
        if (pos === text.length) return true;
        var charBefore = text.slice(pos - 1, pos);
        return charBefore === " " || charBefore === "\n";
    }

    function requestAICompletion() {
        suggestionCursorPos = editor.selectionStart; // Capture cursor position for insertion
        var textBefore = editor.value.substring(0, suggestionCursorPos);
        var textAfter = editor.value.substring(suggestionCursorPos);

        setAIStatus("loading", "✨ Thinking...");
        fetch("/api/complete", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ textBefore: textBefore, textAfter: textAfter })
        })
        .then(function(response) {
            if (!response.ok) throw new Error("HTTP " + response.status);
            return response.body;
        })
        .then(function(stream) {
            var reader = stream.getReader();
            var decoder = new TextDecoder();
            currentSuggestion = "";
            function readChunk() {
                reader.read().then(function(result) {
                    if (result.done) { setAIStatus("ready", "✨ AI Ready"); return; }
                    var chunk = decoder.decode(result.value, { stream: true });
                    var lines = chunk.split("\n");
                    for (var i = 0; i < lines.length; i++) {
                        var line = lines[i];
                        if (line.indexOf("data: ") === 0) {
                            var data = line.slice(6);
                            if (data === "[DONE]") { setAIStatus("ready", "✨ Tab to accept"); return; }
                            try {
                                var parsed = JSON.parse(data);
                                if (parsed.token) { currentSuggestion += parsed.token; showSuggestion(currentSuggestion); }
                                else if (parsed.error) { setAIStatus("error", "⚠ " + parsed.error); }
                            } catch (e) {}
                        }
                    }
                    readChunk();
                });
            }
            readChunk();
        })
        .catch(function() { setAIStatus("error", "⚠ AI Error"); });
    }

    function showSuggestion(text) {
        // Render ghost text: textBefore + suggestion (greyed) + textAfter
        var cursorPos = suggestionCursorPos >= 0 ? suggestionCursorPos : editor.value.length;
        var textBefore = editor.value.substring(0, cursorPos);
        var textAfter = editor.value.substring(cursorPos);

        // Use innerHTML with a span for the suggestion styling
        var escapedBefore = escapeHtml(textBefore);
        var escapedSuggestion = escapeHtml(text);
        var escapedAfter = escapeHtml(textAfter);

        ghostText.innerHTML = escapedBefore +
            '<span class="ghost-suggestion">' + escapedSuggestion + '</span>' +
            escapedAfter;
        ghostText.classList.add("has-suggestion");
    }

    function escapeHtml(str) {
        return str.replace(/&/g, "&amp;")
                  .replace(/</g, "&lt;")
                  .replace(/>/g, "&gt;")
                  .replace(/"/g, "&quot;")
                  .replace(/\n/g, "\n"); // preserve newlines for pre-wrap
    }

    function clearSuggestion() {
        currentSuggestion = "";
        suggestionCursorPos = -1;
        ghostText.innerHTML = "";
        ghostText.classList.remove("has-suggestion");
    }

    function acceptSuggestion() {
        if (!currentSuggestion) return;

        // Insert at the cursor position where suggestion was requested
        var insertPos = suggestionCursorPos >= 0 ? suggestionCursorPos : editor.value.length;

        // Build new editor value
        var before = editor.value.substring(0, insertPos);
        var after = editor.value.substring(insertPos);

        // Capture old value for diffing
        oldValue = editor.value;
        editor.value = before + currentSuggestion + after;
        var newCursorPos = insertPos + currentSuggestion.length;
        editor.selectionStart = editor.selectionEnd = newCursorPos;

        // Generate ops via diff
        var ops = diffToOps(oldValue, editor.value, newCursorPos);
        for (var i = 0; i < ops.length; i++) {
            sendOp(ops[i]);
        }

        oldValue = editor.value;
        clearSuggestion();
        updateCharCount();
        setAIStatus("ready", "✨ AI Ready");
        addLog("AI", "Suggestion accepted");
        editor.focus();
    }

    editor.addEventListener("keydown", function(e) {
        if (e.key === "Tab" && currentSuggestion) { e.preventDefault(); acceptSuggestion(); }
        else if (e.key === "Escape") { clearSuggestion(); setAIStatus("ready", "✨ AI Ready"); }
        else if (e.key === "ArrowRight" && currentSuggestion && editor.selectionStart === editor.value.length) { e.preventDefault(); acceptSuggestion(); }
    });

    editor.addEventListener("scroll", function() {
        ghostText.scrollTop = editor.scrollTop;
        renderRemoteCursors();
    });

    // ==========================================
    //   UI Helpers
    // ==========================================
    function renderUsers(users) {
        avatarsDiv.innerHTML = "";
        for (var i = 0; i < users.length; i++) {
            var u = users[i];
            var div = document.createElement("div");
            div.className = "avatar";
            div.style.background = u.color;
            div.textContent = u.name.charAt(0).toUpperCase();
            div.innerHTML += '<span class="tooltip">' + u.name + '</span>';
            avatarsDiv.appendChild(div);
        }
        statusText.textContent = users.length + " editing";
    }

    function addLog(who, text) {
        var entry = document.createElement("div");
        entry.className = "log-entry";
        entry.innerHTML = '<span class="who">' + who + '</span> ' + text;
        logContainer.prepend(entry);
        while (logContainer.children.length > 50) logContainer.removeChild(logContainer.lastChild);
    }

    function updateCharCount() {
        var count = editor.value.length;
        var words = editor.value.trim() ? editor.value.trim().split(/\s+/).length : 0;
        var bufferInfo = offlineBuffer.length > 0 ? " | " + offlineBuffer.length + " buffered" : "";
        charCountEl.textContent = count + " chars | " + words + " words" + bufferInfo;
    }

    function setAIStatus(type, msg) {
        aiBadge.textContent = msg;
        aiBadge.className = "ai-badge " + type;
    }

    // --- Init ---
    function initApp() {
        fetch("/health").then(function(r) { return r.json(); }).then(function(data) {
            if (data.status === "no_api_key") setAIStatus("error", "⚠ AI disabled — no key");
            else setAIStatus("ready", "✨ AI Ready");
        }).catch(function() { setAIStatus("error", "⚠ Server error"); });

        oldValue = editor.value;
        connect();
    }
})();
