// Room phone controller — reuses track-timeline.js UI helpers (steal modal,
// exact-year toggle, play/skip/buy, fragment refresh) but sits on the room
// websocket instead of the lobby hub. Host TV owns YouTube audio; this page
// only POSTs play/pause/resume and unlocks listen gates when song: arrives.

let roomPhoneCode = null;
let roomPhoneConn = null;
let roomHostPlaying = false;
let roomPlaceMode = false;

function roomPhoneRoot() {
    return document.getElementById("room-phone");
}

function roomEnterPlaceMode() {
    const root = roomPhoneRoot();
    if (!root) return;
    roomPlaceMode = true;
    root.classList.add("is-placing");
    const listen = document.getElementById("room-phone-listen");
    const place = document.getElementById("room-phone-place");
    if (listen) listen.hidden = true;
    if (place) place.hidden = false;
    roomPhoneSyncBoardVisibility();
    if (typeof syncPlacementButtons === "function") syncPlacementButtons();
    if (typeof ttValidateExactYear === "function") ttValidateExactYear();
}

function roomExitPlaceMode() {
    const root = roomPhoneRoot();
    if (!root) return;
    roomPlaceMode = false;
    root.classList.remove("is-placing");
    const listen = document.getElementById("room-phone-listen");
    const place = document.getElementById("room-phone-place");
    if (listen) listen.hidden = false;
    if (place) place.hidden = true;
    const useExact = document.getElementById("tt-use-exact-year");
    if (useExact && useExact.checked) {
        useExact.checked = false;
        if (typeof ttToggleExactYear === "function") ttToggleExactYear();
    }
    roomPhoneSyncBoardVisibility();
}

function roomPhoneSyncGuessYearBtn() {
    const btn = document.getElementById("tt-guess-year-btn");
    if (!btn) return;
    const played = !!ttPlaybackStartedThisRound;
    const gateOpen = typeof ttListenGateSatisfied === "function" ? ttListenGateSatisfied() : true;
    const ready = played && gateOpen;
    btn.disabled = !ready;
    if (!played) {
        btn.title = "Play the song first";
    } else if (!gateOpen) {
        btn.title = "Give everyone a chance to guess — " +
            (typeof ttListenGateRemainingSeconds === "function" ? ttListenGateRemainingSeconds() + "s left" : "wait a moment");
    } else {
        btn.title = "Choose where this song goes on your timeline";
    }
}

function roomPhoneSyncBoardVisibility() {
    const board = document.querySelector(".room-phone-board");
    if (!board) return;
    const steal = !!board.querySelector('[hx-post*="attempt-steal"]');
    if (steal && !roomPlaceMode) {
        roomEnterPlaceMode();
        return;
    }
    // Timeline only on the Guess year / steal screen — not during listen controls.
    board.classList.toggle("is-deferred", !steal && !roomPlaceMode);
}

function initRoomPhone(code, lobbyId) {
    roomPhoneCode = code;
    ttLobbyId = lobbyId;
    roomPlaceMode = false;

    // Phones never load the real IFrame API. Stub YT.PlayerState so shared
    // track-timeline.js (ttPlayPauseClick compares YT.PlayerState.*) does not
    // throw, and stub ttPlayer so syncPlaybackUI can read play/pause state.
    //
    // Important: before the first play this round the stub must look like
    // UNSTARTED (-1), not PAUSED (2). ttPlayPauseClick maps PAUSED → resume-song
    // and only UNSTARTED/other → play-song; a PAUSED default would never cue
    // the host TV clip.
    if (typeof window.YT === "undefined") {
        window.YT = {
            PlayerState: { UNSTARTED: -1, ENDED: 0, PLAYING: 1, PAUSED: 2, BUFFERING: 3, CUED: 5 },
        };
    }
    ttPlayerReady = true;
    ttPlayer = {
        getPlayerState: function () {
            if (roomHostPlaying) return YT.PlayerState.PLAYING;
            if (ttPlaybackStartedThisRound) return YT.PlayerState.PAUSED;
            return YT.PlayerState.UNSTARTED;
        },
        playVideo: function () {},
        pauseVideo: function () {},
        stopVideo: function () {},
        loadVideoById: function () {},
    };

    document.body.addEventListener("htmx:afterSwap", function (event) {
        if (!event.detail || !event.detail.target) return;
        const id = event.detail.target.id;
        if (id === "tt-board") {
            ttSyncSelfStatus();
            syncPlaybackUI();
            roomPhoneSyncBoardVisibility();
            roomPhoneSyncGuessYearBtn();
        }
        if (id === "tt-current-card") {
            // Fragment refresh rebuilds listen/place panels — re-apply mode
            // only while the place panel still exists (gone after place/reveal).
            if (roomPlaceMode && document.getElementById("room-phone-place")) {
                roomEnterPlaceMode();
            } else {
                roomExitPlaceMode();
            }
            syncPlaybackUI();
            roomPhoneSyncBoardVisibility();
            roomPhoneSyncGuessYearBtn();
        }
    });

    connectRoomPhoneSocket();
    roomPhoneSyncBoardVisibility();
    roomPhoneSyncGuessYearBtn();
}

function connectRoomPhoneSocket() {
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    roomPhoneConn = new WebSocket(protocol + "//" + location.host + "/ws/room/" + roomPhoneCode + "?role=seat");
    roomPhoneConn.onmessage = function (e) {
        roomPhoneOnMessage(e.data);
    };
    roomPhoneConn.onclose = function () {
        setTimeout(connectRoomPhoneSocket, 1500);
    };
}

function roomPhoneOnMessage(message) {
    // The room was deleted: stop retrying a socket that can never reconnect.
    if (message === "kick") {
        roomPhoneConn.onclose = null;
        location.href = "/";
        return;
    }
    if (message === "refresh") {
        // Waiting phones have no #tt-current-card/#tt-board yet — htmx.ajax at a
        // missing target throws and spamsthe console when seats join.
        if (document.getElementById("tt-current-card") || document.getElementById("tt-board")) {
            refreshGame();
        }
        // Someone sat down: update the waiting list of who has joined.
        if (document.getElementById("room-phone-players")) {
            document.body.dispatchEvent(new Event("room-refresh"));
        }
        return;
    }
    if (message === "reload") {
        setTimeout(function () {
            location.reload();
        }, 400);
        return;
    }
    if (message === "paused") {
        const el = document.getElementById("room-paused-banner");
        if (el) el.hidden = false;
        return;
    }
    if (message === "resumed") {
        const el = document.getElementById("room-paused-banner");
        if (el) el.hidden = true;
        refreshGame();
        return;
    }
    if (message.startsWith("status:")) {
        const el = document.getElementById("room-phone-status");
        if (el) el.textContent = message.slice(7);
        showStatus(message.slice(7));
        return;
    }
    if (message.startsWith("alert:")) {
        showStatus(message.slice(6));
        return;
    }
    if (message.startsWith("song:")) {
        // A new song is starting, so last round's result is stale.
        roomPhoneDismissResult();
        // Host display plays the clip; phones only unlock listen/skip/place gates.
        roomHostPlaying = true;
        ttPlaybackStartedThisRound = true;
        ttStartListenGate();
        ttHoldTimerForPlayback();
        syncPlaybackUI();
        roomPhoneSyncBoardVisibility();
        roomPhoneSyncGuessYearBtn();
        return;
    }
    if (message === "songStop") {
        roomHostPlaying = false;
        stopSong();
        // Placement locked — leave Guess-year screen so the next refresh
        // starts on listen controls (or Watch the TV) rather than an empty place panel.
        roomExitPlaceMode();
        roomPhoneSyncBoardVisibility();
        return;
    }
    if (message === "songPause") {
        roomHostPlaying = false;
        ttClipListenedThisRound = true;
        ttReleaseTimerAfterPlayback();
        syncPlaybackUI();
        return;
    }
    if (message === "songResume") {
        roomHostPlaying = true;
        ttHoldTimerForPlayback();
        syncPlaybackUI();
        return;
    }
    if (message.startsWith("steal:")) {
        try {
            handleStealJoin(JSON.parse(message.slice(6)));
        } catch (e) {}
        refreshGame();
        return;
    }
    if (message.startsWith("stealTurn:")) {
        try {
            handleStealTurn(JSON.parse(message.slice(10)));
        } catch (e) {}
        refreshGame();
        return;
    }
    if (message.startsWith("result:")) {
        try {
            const payload = JSON.parse(message.slice(7));
            ttCloseStealModal();
            if (payload.bottomMessage) showStatus(payload.bottomMessage);
            roomPhoneShowResult(payload);
        } catch (e) {}
        roomHostPlaying = false;
        roomExitPlaceMode();
        stopSong();
        setTimeout(refreshGame, 300);
        return;
    }
}

// ---- Brief result popup ----------------------------------------------------
// The same facts the TV shows after a round, for the phone in your hand: whether
// the turn player was right, what they guessed, then the song and its year. It
// clears itself, on a tap, or when the next song starts.

const ROOM_PHONE_RESULT_MS = 5000;
const ROOM_PHONE_GAME_OVER_MS = 8000;
let roomPhoneResultTimer = null;

// Guest seats carry a "·code" suffix to keep their account names unique.
function roomPhoneCleanName(name) {
    return String(name || "").split("·")[0];
}

function roomPhoneDismissResult() {
    clearTimeout(roomPhoneResultTimer);
    const el = document.getElementById("room-phone-result");
    if (el) el.remove();
}

function roomPhoneShowResult(p) {
    roomPhoneDismissResult();
    if (!p || !p.title) return;

    const turn = roomPhoneCleanName(p.turnPlayerName);
    const mine = turn !== "" && turn === roomPhoneCleanName(window.roomPhoneMe);
    const possessive = mine ? "Your" : turn + "'s";

    const lines = [];
    const add = (cls, text) => lines.push({ cls: cls, text: text });
    if (turn) {
        add(p.turnPlayerCorrect ? "rpr-verdict is-right" : "rpr-verdict is-wrong", p.turnPlayerCorrect ? "Right!" : "Wrong");
        const guesses = [];
        if (p.turnPlayerRange) guesses.push("placed in " + p.turnPlayerRange);
        if (p.turnPlayerExactYear) {
            guesses.push("exact year " + p.turnPlayerExactYear + (p.turnPlayerExactYear === p.releaseYear ? " (nailed it)" : " (missed)"));
        }
        if (guesses.length) add("rpr-guess", possessive + " guess: " + guesses.join(", "));
    }
    if (p.type === "won" && p.wonByChallenge && p.winnerName) add("rpr-note", roomPhoneCleanName(p.winnerName) + " stole it!");
    if (p.releaseYear) add("rpr-year", String(p.releaseYear));
    add("rpr-artist", p.artist);
    add("rpr-song", "“" + p.title + "”");
    if (p.gameOver && p.winnerName) add("rpr-note", roomPhoneCleanName(p.winnerName) + " wins!");

    const overlay = document.createElement("div");
    overlay.id = "room-phone-result";
    overlay.className = "room-phone-result";
    const card = document.createElement("div");
    card.className = "room-phone-result-card";
    lines.forEach((line) => {
        const div = document.createElement("div");
        div.className = line.cls;
        div.textContent = line.text;
        card.appendChild(div);
    });
    overlay.appendChild(card);
    overlay.addEventListener("click", roomPhoneDismissResult);
    document.body.appendChild(overlay);
    roomPhoneResultTimer = setTimeout(roomPhoneDismissResult, p.gameOver ? ROOM_PHONE_GAME_OVER_MS : ROOM_PHONE_RESULT_MS);
}

// ---- Room voice guess (Web Speech API → editable fields → Lock guess) ------

let roomSpeechRecognition = null;
let roomSpeechListening = false;

function roomSpeechSupported() {
    return !!(window.SpeechRecognition || window.webkitSpeechRecognition);
}

function roomSetVoiceStatus(text, show) {
    const el = document.getElementById("tt-voice-status");
    if (!el) return;
    el.textContent = text || "";
    el.hidden = !show;
}

// What the guess form shows depends on whether anything has been entered:
// Lock guess only once there is something to lock, and exactly one of Hold to
// speak / Re-record (Re-record once a spoken guess has filled the boxes).
let roomVoiceRecorded = false;

function roomPhoneSyncGuessUI() {
    const title = document.getElementById("tt-guess-title");
    const artist = document.getElementById("tt-guess-artist");
    const hasText = !!((title && title.value.trim()) || (artist && artist.value.trim()));
    if (!hasText) roomVoiceRecorded = false;

    const lock = document.getElementById("tt-lock-guess");
    if (lock) lock.hidden = !hasText;
    const hold = document.getElementById("tt-hold-mic");
    const reRecord = document.getElementById("tt-re-record");
    if (hold) hold.style.display = roomVoiceRecorded ? "none" : "";
    if (reRecord) reRecord.style.display = roomVoiceRecorded ? "" : "none";
}

document.addEventListener("input", function (e) {
    if (e.target && (e.target.id === "tt-guess-title" || e.target.id === "tt-guess-artist")) {
        roomPhoneSyncGuessUI();
    }
});
document.addEventListener("htmx:afterSwap", function (e) {
    if (e.detail && e.detail.target && e.detail.target.id === "tt-current-card") roomPhoneSyncGuessUI();
});

// roomSetMicLabel puts the listening/idle label on whichever mic button is
// showing (Hold to speak, or Re-record once a guess was spoken).
function roomSetMicLabel(listening) {
    const hold = document.getElementById("tt-hold-mic");
    const reRecord = document.getElementById("tt-re-record");
    if (hold) hold.innerHTML = listening ? "Listening…" : '<span class="bi bi-mic"></span> Hold to speak';
    if (reRecord) reRecord.innerHTML = listening ? "Listening…" : '<span class="bi bi-mic"></span> Re-record';
}

function roomApplyHeardText(transcript) {
    const title = document.getElementById("tt-guess-title");
    const artist = document.getElementById("tt-guess-artist");
    if (!title) return;

    let heard = (transcript || "").trim();
    if (!heard) return;

    // Split on " by " when both fields exist; otherwise dump into title.
    if (artist) {
        const match = heard.match(/^(.*?)\s+by\s+(.+)$/i);
        if (match) {
            title.value = match[1].trim();
            artist.value = match[2].trim();
        } else {
            title.value = heard;
        }
    } else {
        title.value = heard;
    }

    roomVoiceRecorded = true;
    roomPhoneSyncGuessUI();
    roomSetVoiceStatus("Edit if needed, then Lock guess.", true);
}

function roomHoldMic(event) {
    if (event) event.preventDefault();
    if (!roomSpeechSupported()) {
        roomSetVoiceStatus("Voice not supported here — type your guess instead.", true);
        return;
    }

    if (roomSpeechListening && roomSpeechRecognition) {
        try { roomSpeechRecognition.stop(); } catch (e) {}
        return;
    }

    const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
    roomSpeechRecognition = new SpeechRecognition();
    roomSpeechRecognition.lang = "en-US";
    roomSpeechRecognition.interimResults = true;
    roomSpeechRecognition.continuous = false;
    roomSpeechRecognition.maxAlternatives = 1;

    let finalText = "";
    roomSpeechListening = true;
    roomSetVoiceStatus("Listening… release when done.", true);
    roomSetMicLabel(true);

    roomSpeechRecognition.onresult = function (ev) {
        let interim = "";
        for (let i = ev.resultIndex; i < ev.results.length; i++) {
            const piece = ev.results[i][0].transcript;
            if (ev.results[i].isFinal) {
                finalText += piece + " ";
            } else {
                interim += piece;
            }
        }
        roomSetVoiceStatus((finalText || interim || "Listening…").trim(), true);
    };

    roomSpeechRecognition.onerror = function () {
        roomSpeechListening = false;
        roomSetMicLabel(false);
        roomSetVoiceStatus("Could not hear that — try again or type.", true);
    };

    roomSpeechRecognition.onend = function () {
        roomSpeechListening = false;
        roomSetMicLabel(false);
        if (finalText.trim()) {
            roomApplyHeardText(finalText.trim());
        } else {
            roomSetVoiceStatus("Nothing heard — try again or type.", true);
        }
    };

    try {
        roomSpeechRecognition.start();
    } catch (e) {
        roomSpeechListening = false;
        roomSetVoiceStatus("Mic unavailable — type your guess instead.", true);
    }
}
