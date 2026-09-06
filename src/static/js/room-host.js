// Room host display — sole YouTube audio authority + board/log/popups.
let roomHostCode = null;
let roomHostLobbyId = null;
let roomHostConn = null;
let roomYtPlayer = null;
let roomYtReady = false;
let roomAudioUnlocked = false;

function initRoomHost(code, lobbyId) {
    roomHostCode = code;
    roomHostLobbyId = lobbyId;
    connectRoomHostSocket();
    loadRoomYouTubeApi();
}

function connectRoomHostSocket() {
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    roomHostConn = new WebSocket(protocol + "//" + location.host + "/ws/room/" + roomHostCode + "?role=host");
    roomHostConn.onmessage = (e) => roomHostOnMessage(e.data);
    roomHostConn.onclose = () => {
        roomHostAppendLog("Socket closed — reconnecting…");
        setTimeout(connectRoomHostSocket, 1500);
    };
}

function roomHostOnMessage(message) {
    if (message === "refresh" || message === "reload") {
        document.body.dispatchEvent(new Event("room-refresh"));
        return;
    }
    if (message === "paused") {
        const el = document.getElementById("room-paused");
        if (el) el.style.display = "";
        roomHostAppendLog("Paused — host display disconnected");
        return;
    }
    if (message === "resumed") {
        const el = document.getElementById("room-paused");
        if (el) el.style.display = "none";
        roomHostAppendLog("Host display back — resumed");
        document.body.dispatchEvent(new Event("room-refresh"));
        return;
    }
    if (message.startsWith("log:")) {
        roomHostAppendLog(message.slice(4));
        return;
    }
    if (message.startsWith("status:")) {
        const el = document.getElementById("room-status");
        if (el) el.textContent = message.slice(7);
        roomHostAppendLog(message.slice(7));
        return;
    }
    if (message.startsWith("song:")) {
        try { roomHostPlaySong(JSON.parse(message.slice(5))); } catch (e) {}
        return;
    }
    if (message === "songStop" || message === "songPause") {
        try { if (roomYtPlayer) roomYtPlayer.pauseVideo(); } catch (e) {}
        return;
    }
    if (message === "songResume") {
        try { if (roomYtPlayer) roomYtPlayer.playVideo(); } catch (e) {}
        return;
    }
    if (message.startsWith("steal:")) {
        try {
            const s = JSON.parse(message.slice(6));
            roomHostAppendLog(s.placerName ? (s.placerName + " placed — steal window open") : "Steal window open");
        } catch (e) {
            roomHostAppendLog("Steal window open");
        }
        document.body.dispatchEvent(new Event("room-refresh"));
        return;
    }
    if (message.startsWith("stealTurn:")) {
        try {
            const s = JSON.parse(message.slice(10));
            roomHostAppendLog((s.stealerName || "Someone") + " is stealing");
        } catch (e) {
            roomHostAppendLog("Steal attempt");
        }
        document.body.dispatchEvent(new Event("room-refresh"));
        return;
    }
    if (message.startsWith("result:")) {
        try {
            const r = JSON.parse(message.slice(7));
            const parts = [];
            if (r.title) {
                parts.push(r.title + (r.artist ? " · " + r.artist : "") + (r.releaseYear ? " · " + r.releaseYear : ""));
            }
            if (r.guessTokenGuessText) parts.push("Guessed: “" + r.guessTokenGuessText + "”");
            if (r.bottomMessage) parts.push(r.bottomMessage);
            roomHostShowPopup(r.gameOver ? "Game over" : "Reveal", parts.join("<br>"));
        } catch (e) {}
        document.body.dispatchEvent(new Event("room-refresh"));
        return;
    }
}

function roomHostAppendLog(text) {
    const list = document.getElementById("room-log-list");
    if (!list) return;
    const li = document.createElement("li");
    // Match gameshell chat markup: <blue>/<green>/<red> … </>
    const raw = String(text || "")
        .replaceAll("<red>", '<span class="gs-chat-red">')
        .replaceAll("<green>", '<span class="gs-chat-green">')
        .replaceAll("<blue>", '<span class="gs-chat-blue">')
        .replaceAll("</>", "</span>");
    li.innerHTML = raw;
    list.prepend(li);
    while (list.children.length > 80) list.removeChild(list.lastChild);
}

function roomHostShowPopup(title, bodyHtml) {
    document.getElementById("room-popup-title").textContent = title;
    document.getElementById("room-popup-body").innerHTML = bodyHtml;
    document.getElementById("room-popup").hidden = false;
}

function roomHostDismissPopup() {
    document.getElementById("room-popup").hidden = true;
}

function roomHostUnlockAudio() {
    roomAudioUnlocked = true;
    const btn = document.getElementById("tt-audio-unlock");
    if (btn) btn.style.display = "none";
    if (roomYtPlayer) {
        try { roomYtPlayer.playVideo(); roomYtPlayer.pauseVideo(); } catch (e) {}
    }
}

function loadRoomYouTubeApi() {
    if (window.YT && window.YT.Player) {
        roomHostSetupPlayer();
        return;
    }
    const prev = window.onYouTubeIframeAPIReady;
    window.onYouTubeIframeAPIReady = function () {
        if (typeof prev === "function") prev();
        roomHostSetupPlayer();
    };
    if (!document.getElementById("tt-youtube-api")) {
        const tag = document.createElement("script");
        tag.id = "tt-youtube-api";
        tag.src = "https://www.youtube.com/iframe_api";
        document.head.appendChild(tag);
    }
}

function roomHostSetupPlayer() {
    roomYtPlayer = new YT.Player("tt-youtube-player", {
        height: "1",
        width: "1",
        playerVars: { autoplay: 0, controls: 0, rel: 0 },
        events: { onReady: () => { roomYtReady = true; } }
    });
    const btn = document.getElementById("tt-audio-unlock");
    if (btn) btn.style.display = "";
}

function roomHostPlaySong(song) {
    if (!roomYtReady || !roomYtPlayer) return;
    const opts = { videoId: song.videoId, startSeconds: song.startSeconds || 0 };
    if (song.endSeconds) opts.endSeconds = song.endSeconds;
    try {
        roomYtPlayer.loadVideoById(opts);
        if (roomAudioUnlocked) roomYtPlayer.playVideo();
    } catch (e) {}
}
