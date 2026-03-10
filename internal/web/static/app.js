// === SyncWave Client: CRDT Shadow + Collaboration + AI + Offline Sync ===

(function() {
    "use strict";

    var editor = document.getElementById("editor");
    var ghostText = document.getElementById("ghost-text");
    var statusDot = document.getElementById("statusDot");
    var statusText = document.getElementById("statusText");
    var avatarsDiv = document.getElementById("avatars");
    var logContainer = document.getElementById("logContainer");
    var charCountEl = document.getElementById("charCount");
    var activityBtn = document.getElementById("activityBtn");
    var activityPanel = document.getElementById("activityPanel");
    var aiBadge = document.getElementById("aiBadge");
    var offlineBanner = document.getElementById("offlineBanner");
    var cursorsOverlay = document.getElementById("cursors-overlay");
    var nameModal = document.getElementById("nameModal");
    var nameInput = document.getElementById("nameInput");
    var nameSubmit = document.getElementById("nameSubmit");

    // --- Identity ---
    var userID = "U-" + Math.random().toString(36).substring(2, 6).toUpperCase();
    var userName = "Anonymous";
    var myColor = "#7a5cff";
    var isRemoteUpdate = false;
    var started = false;

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
    var ws = null;
    var connected = false;
    var lastSeq = 0;
    var offlineBuffer = [];
    var reconnectAttempts = 0;
    var reconnectTimer = null;
    var pendingRestore = false;

    // The editor content at the moment of last disconnect — used as the "base"
    // for three-way merge when reconnecting with offline edits.
    var lastSyncedContent = null;
    var wasDisconnected = false;

    // --- Local CRDT Shadow ---
    // Ordered array of { id: {clock, siteID}, char: string }
    // Mirrors the server's visible document state for position<->OpID mapping.
    var shadow = [];
    var placeholderCounter = 0;
    var ROOT_ID = { clock: 0, siteID: "ROOT" };

    // --- Client-side health check ---
    var healthCheckTimer = null;
    var lastMessageTime = 0;
    var appPingTimer = null;

    function startHealthCheck() {
        stopHealthCheck();
        lastMessageTime = Date.now();

        // Application-level ping every 25s (belt-and-suspenders with WS pings)
        appPingTimer = setInterval(function() {
            if (connected && ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({ type: "ping" }));
            }
        }, 25000);

        // If no message from server in 70s, force reconnect
        healthCheckTimer = setInterval(function() {
            if (!connected || !ws || ws.readyState !== WebSocket.OPEN) return;
            if (Date.now() - lastMessageTime > 70000) {
                console.log("[SyncWave] No server activity for 70s, reconnecting...");
                addLog("System", "Connection stale — reconnecting...");
                ws.close();
            }
        }, 15000);
    }

    function stopHealthCheck() {
        if (healthCheckTimer) { clearInterval(healthCheckTimer); healthCheckTimer = null; }
        if (appPingTimer) { clearInterval(appPingTimer); appPingTimer = null; }
    }

    // --- Three-way merge ---
    function mergeTexts(base, local, server) {
        if (!base) return server;
        if (base === local) return server;
        if (local === server) return server;

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
        if (ourInserted.length === 0) return server;

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

        var insertPos;
        if (prefix < sharedPrefix) {
            insertPos = prefix;
        } else if (prefix >= base.length - sharedSuffix) {
            insertPos = (server.length - sharedSuffix) + (prefix - (base.length - sharedSuffix));
        } else {
            insertPos = server.length - sharedSuffix;
        }

        return server.substring(0, insertPos) + ourInserted + server.substring(insertPos);
    }

    // --- Shadow CRDT helpers ---
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
    var aiDebounceTimer = null;
    var currentSuggestion = "";
    var suggestionCursorPos = -1;

    // --- Remote Cursors ---
    var remoteCursors = {};

    // --- Old Value Tracking ---
    var oldValue = "";

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
            startHealthCheck();

            ws.send(JSON.stringify({
                type: "join",
                userID: userID,
                userName: userName,
                lastSeq: lastSeq
            }));
        };

        ws.onclose = function() {
            if (connected) {
                lastSyncedContent = editor.value;
            }
            connected = false;
            wasDisconnected = true;
            pendingRestore = false;
            setConnectionStatus("offline");
            stopHealthCheck();
            addLog("System", "Connection lost — edits saved locally");
            scheduleReconnect();
        };

        ws.onerror = function() {};

        ws.onmessage = function(event) {
            lastMessageTime = Date.now();

            var msg;
            try { msg = JSON.parse(event.data); } catch(e) { return; }

            // Application-level pong — no action needed, lastMessageTime already updated
            if (msg.type === "pong") return;

            if (msg.seq) lastSeq = msg.seq;

            switch (msg.type) {
                case "full_sync":
                    isRemoteUpdate = true;
                    var savedCursor = editor.selectionStart;
                    var serverContent = (msg.content != null) ? msg.content : "";
                    var localText = editor.value;
                    myColor = msg.color || myColor;

                    // ---- RESTORE RESPONSE ----
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
                        var merged2 = mergeTexts(lastSyncedContent, localText, serverContent);
                        rebuildShadow(serverContent, msg.nodeIDs || []);
                        offlineBuffer = [];
                        var freshOps2 = diffToOps(serverContent, merged2, merged2.length);

                        if (freshOps2.length > 0) {
                            editor.value = merged2;
                            oldValue = merged2;
                            addLog("System", "Merging " + freshOps2.length + " offline changes...");
                            ws.send(JSON.stringify({
                                type: "batch_sync",
                                content: JSON.stringify(freshOps2)
                            }));
                        } else {
                            editor.value = serverContent;
                            oldValue = serverContent;
                        }
                    } else {
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
                    if (pendingRestore) break;
                    if (msg.userID === userID) {
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

                    if (msg.newID) {
                        shadowInsert(insPos, insCh, msg.newID);
                    }

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
                    if (msg.userID === userID) break;
                    isRemoteUpdate = true;
                    var dpos = (msg.position != null) ? msg.position : -1;
                    if (dpos >= 0 && dpos < editor.value.length) {
                        var dss = editor.selectionStart;
                        var dse = editor.selectionEnd;

                        shadowDelete(dpos);

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
        stopHealthCheck();
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
    editor.addEventListener("beforeinput", function() {
        oldValue = editor.value;
    });

    editor.addEventListener("input", function() {
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

    function diffToOps(oldVal, newVal, cursorAfter) {
        var ops = [];

        var prefixLen = 0;
        var minLen = Math.min(oldVal.length, newVal.length);
        while (prefixLen < minLen && oldVal.charAt(prefixLen) === newVal.charAt(prefixLen)) {
            prefixLen++;
        }

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

        // Generate delete ops (reverse order for stable positions)
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

            shadowInsert(insPos, ch, placeholderID);
        }

        return ops;
    }

    // ==========================================
    //   REMOTE CURSOR RENDERING
    // ==========================================
    function broadcastCursor() {
        if (!connected) return;
        sendOp({ type: "cursor", position: editor.selectionStart });
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

            var cursorEl = document.createElement("div");
            cursorEl.className = "remote-cursor";
            cursorEl.style.left = coords.left + "px";
            cursorEl.style.top = coords.top + "px";
            cursorEl.style.height = coords.height + "px";
            cursorEl.style.backgroundColor = cursor.color;

            var label = document.createElement("div");
            label.className = "remote-cursor-label";
            label.style.backgroundColor = cursor.color;
            label.textContent = cursor.userName;
            cursorEl.appendChild(label);

            cursorsOverlay.appendChild(cursorEl);
        }
    }

    function getCaretCoordinates(pos) {
        var mirror = document.getElementById("caret-mirror");
        if (!mirror) {
            mirror = document.createElement("div");
            mirror.id = "caret-mirror";
            mirror.style.cssText = "position:absolute;top:0;left:0;visibility:hidden;white-space:pre-wrap;word-wrap:break-word;overflow:hidden;pointer-events:none;";
            document.getElementById("editor-host").appendChild(mirror);
        }

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
        suggestionCursorPos = editor.selectionStart;
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
                            } catch (e) { /* ignore parse errors from partial chunks */ }
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
        var cursorPos = suggestionCursorPos >= 0 ? suggestionCursorPos : editor.value.length;
        var textBefore = editor.value.substring(0, cursorPos);
        var textAfter = editor.value.substring(cursorPos);

        ghostText.innerHTML = escapeHtml(textBefore) +
            '<span class="ghost-suggestion">' + escapeHtml(text) + '</span>' +
            escapeHtml(textAfter);
        ghostText.classList.add("has-suggestion");
    }

    function escapeHtml(str) {
        return str.replace(/&/g, "&amp;")
                  .replace(/</g, "&lt;")
                  .replace(/>/g, "&gt;")
                  .replace(/"/g, "&quot;");
    }

    function clearSuggestion() {
        currentSuggestion = "";
        suggestionCursorPos = -1;
        ghostText.innerHTML = "";
        ghostText.classList.remove("has-suggestion");
    }

    function acceptSuggestion() {
        if (!currentSuggestion) return;

        var insertPos = suggestionCursorPos >= 0 ? suggestionCursorPos : editor.value.length;
        var before = editor.value.substring(0, insertPos);
        var after = editor.value.substring(insertPos);

        oldValue = editor.value;
        editor.value = before + currentSuggestion + after;
        var newCursorPos = insertPos + currentSuggestion.length;
        editor.selectionStart = editor.selectionEnd = newCursorPos;

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
