// --- SyncWave Client ---

const editor = document.getElementById("editor");
const statusDot = document.getElementById("statusDot");
const statusText = document.getElementById("statusText");
const userBadge = document.getElementById("userBadge");
const logContainer = document.getElementById("logContainer");

// Generate a unique user ID
const userID = "User-" + Math.random().toString(36).substring(2, 6).toUpperCase();
userBadge.textContent = userID;

let isRemoteUpdate = false; // Flag to ignore events triggered by remote updates
let clock = 0;

// --- WebSocket ---
const ws = new WebSocket("ws://" + location.host + "/ws");

ws.onopen = () => {
    statusDot.classList.add("connected");
    statusText.textContent = "Connected";
    addLog("System", "Connected to SyncWave server");
};

ws.onclose = () => {
    statusDot.classList.remove("connected");
    statusText.textContent = "Disconnected";
    addLog("System", "Disconnected from server");
};

ws.onerror = () => {
    statusText.textContent = "Connection Error";
};

// --- Receiving Remote Edits ---
ws.onmessage = (event) => {
    const msg = JSON.parse(event.data);

    if (msg.type === "insert") {
        isRemoteUpdate = true;
        const char = String.fromCharCode(msg.char);
        
        // Insert at end for simplicity in this demo
        const pos = editor.selectionStart;
        editor.value += char;
        
        // Update remote clock
        if (msg.clock > clock) {
            clock = msg.clock;
        }

        addLog(msg.siteID, "typed '" + char + "'");
        isRemoteUpdate = false;
    } else if (msg.type === "full_sync") {
        isRemoteUpdate = true;
        editor.value = msg.fullText;
        addLog(msg.siteID, "synced document");
        isRemoteUpdate = false;
    }
};

// --- Sending Local Edits ---
editor.addEventListener("input", (e) => {
    if (isRemoteUpdate) return;

    if (e.inputType === "insertText" && e.data) {
        clock++;
        const msg = {
            type: "insert",
            char: e.data.charCodeAt(0),
            clock: clock,
            siteID: userID,
            anchorClock: 0,
            anchorSiteID: "ROOT"
        };
        ws.send(JSON.stringify(msg));
    } else if (e.inputType === "insertLineBreak") {
        clock++;
        const msg = {
            type: "insert",
            char: 10, // newline
            clock: clock,
            siteID: userID,
            anchorClock: 0,
            anchorSiteID: "ROOT"
        };
        ws.send(JSON.stringify(msg));
    }
});

// --- Activity Log ---
function addLog(sender, text) {
    const entry = document.createElement("div");
    entry.className = "log-entry";
    entry.innerHTML = '<span class="sender">' + sender + '</span>: ' + text;
    logContainer.prepend(entry);
    
    // Keep only last 50 entries
    while (logContainer.children.length > 50) {
        logContainer.removeChild(logContainer.lastChild);
    }
}
