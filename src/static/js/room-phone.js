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
    if (message === "refresh") {
        // Waiting phones have no #tt-current-card/#tt-board yet — htmx.ajax at a
        // missing target throws and spamsthe console when seats join.
        if (document.getElementById("tt-current-card") || document.getElementById("tt-board")) {
            refreshGame();
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
        } catch (e) {}
        roomHostPlaying = false;
        roomExitPlaceMode();
        stopSong();
        setTimeout(refreshGame, 300);
        return;
    }
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

    const reRecord = document.getElementById("tt-re-record");
    if (reRecord) reRecord.style.display = "";
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
    const mic = document.getElementById("tt-hold-mic");
    if (mic) mic.textContent = "Listening…";

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
        if (mic) mic.innerHTML = '<span class="bi bi-mic"></span> Hold to speak';
        roomSetVoiceStatus("Could not hear that — try again or type.", true);
    };

    roomSpeechRecognition.onend = function () {
        roomSpeechListening = false;
        if (mic) mic.innerHTML = '<span class="bi bi-mic"></span> Hold to speak';
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
