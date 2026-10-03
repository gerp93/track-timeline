// Room host display — sole YouTube audio authority + board/log/popups.
let roomHostCode = null;
let roomHostLobbyId = null;
let roomHostConn = null;
let roomYtPlayer = null;
let roomYtReady = false;
let roomAudioUnlocked = false;
let roomHostPlaying = false;

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
        const wasPaused = el && el.style.display !== "none";
        if (el) el.style.display = "none";
        if (wasPaused) roomHostAppendLog("Host display back — resumed");
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
            roomHostShowPopup(r.gameOver ? "Game over" : "Reveal", parts.join("<br>"), r.gameOver ? ROOM_POPUP_GAME_OVER_MS : ROOM_POPUP_REVEAL_MS);
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

// The reveal clears itself: the TV is across the room, nobody is standing at it
// to click OK. Game over lingers longer since it ends the night.
const ROOM_POPUP_REVEAL_MS = 8000;
const ROOM_POPUP_GAME_OVER_MS = 20000;
let roomPopupTimer = null;

function roomHostShowPopup(title, bodyHtml, durationMs) {
    document.getElementById("room-popup-title").textContent = title;
    document.getElementById("room-popup-body").innerHTML = bodyHtml;
    document.getElementById("room-popup").hidden = false;
    clearTimeout(roomPopupTimer);
    roomPopupTimer = setTimeout(roomHostDismissPopup, durationMs || ROOM_POPUP_REVEAL_MS);
}

function roomHostDismissPopup() {
    clearTimeout(roomPopupTimer);
    document.getElementById("room-popup").hidden = true;
}

// roomHostApplyPlaying points the record, tonearm and equalizer at
// roomHostPlaying. The now-playing fragment is re-fetched on every refresh,
// which replaces those elements, so this also runs after each swap.
function roomHostApplyPlaying() {
    document.querySelectorAll(".tt-record").forEach((el) => el.classList.toggle("is-spinning", roomHostPlaying));
    document.querySelectorAll(".tt-tonearm").forEach((el) => el.classList.toggle("is-active", roomHostPlaying));
    document.querySelectorAll(".room-marquee-eq").forEach((el) => el.classList.toggle("is-active", roomHostPlaying));
}

// The join QR is for the lobby, not the game: it shows while the game waits to
// start and disappears once it is running (and returns if the game is reset).
function roomHostSyncJoinHint() {
    const marquee = document.querySelector(".room-marquee");
    const hint = document.getElementById("room-join-hint");
    if (marquee && hint) hint.hidden = marquee.dataset.status !== "waiting";
}

document.addEventListener("htmx:afterSwap", (e) => {
    if (e.detail && e.detail.target && e.detail.target.id === "tt-current-card") {
        roomHostApplyPlaying();
        roomHostSyncJoinHint();
    }
});

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
        events: {
            onReady: () => { roomYtReady = true; },
            // The record, tonearm and equalizer follow what the player is
            // really doing, not what the game says should be happening: a song
            // that is cued but not started (or blocked by the browser until
            // someone taps to enable sound) must not look like it is playing.
            onStateChange: (e) => {
                roomHostPlaying = e.data === YT.PlayerState.PLAYING;
                roomHostApplyPlaying();
            }
        }
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
