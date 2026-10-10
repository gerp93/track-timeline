// Client for a Track Timeline lobby.
//
// The socket only ever carries short control strings plus one JSON payload for
// the reveal; HTML never comes over it. When something changes, the server says
// so and this file re-fetches the affected fragment over HTTP. That keeps the
// server's broadcast small and means a client that reconnects mid-game repairs
// itself by fetching, rather than by replaying a history it missed.

let ttConn = null;
let ttLobbyId = null;
let ttTurnTimerSeconds = 0;
let ttDeferTimerStart = false;
let ttStatusTimeout = null;

let ttClipStartSeconds = 0;
let ttClipEndSeconds = 0;
let ttClipDurationSeconds = 0;
let ttClipProgressInterval = null;
let ttVolume = 100;
let ttMuted = false;

const TT_STATUS_MESSAGE_MS = 8000;

// How long after the last keystroke the guess boxes are saved to the server.
const TT_GUESS_DRAFT_DELAY_MS = 200;

// ---------------------------------------------------------------- websocket

function initTrackTimeline(lobbyId, turnTimerSeconds) {
    ttLobbyId = lobbyId;
    setTurnTimerSeconds(turnTimerSeconds || 0);

    ttInitVolume();
    loadYouTubeApi();

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    ttConn = new WebSocket(protocol + "//" + window.location.host + "/ws/lobby/" + lobbyId);

    ttConn.onclose = () => {
        // The framework deletes a lobby when its last client disconnects, so a
        // closed socket means this page is looking at something that no longer
        // exists. Leaving is the honest response.
        document.location.href = "/track-timeline/lobbies";
    };

    ttConn.onmessage = (event) => handleMessage(event.data);

    const chatForm = document.getElementById("chat-form");
    const chatInput = document.getElementById("chat-input");
    if (chatForm && chatInput && typeof gsChat !== "undefined") {
        gsChat.wireForm(chatForm, chatInput, ttConn);
    }

    // The board is what says whose turn it is, so the timer restarts whenever
    // the board is swapped in.
    document.body.addEventListener("htmx:afterSwap", (event) => {
        if (event.detail.target && event.detail.target.id === "tt-board") {
            ttLoadChallengeOnce();
            ttSyncSelfStatus();
            syncPlaybackUI();
            restartTurnTimer();
        }
        if (event.detail.target && event.detail.target.id === "tt-current-card") {
            syncPlaybackUI();
            ttSyncGuessCard();
        }
    });
}

function handleMessage(message) {
    switch (message) {
        case "refresh":
            refreshGame();
            return;

        case "reload":
            // Deliberately not location.reload(): that drops the websocket, and
            // if this is the only client the server deletes the now-empty lobby
            // before the reload finishes, destroying the game that was just
            // started.
            setTimeout(() => {
                refreshGame();
                refreshControls();
            }, 500);
            return;

        case "songStop":
            // The song is done with: a placement locked in, or it was skipped.
            // Get whatever is still in the boxes to the server now rather than
            // after the debounce.
            ttFlushGuessDraft();
            stopSong();
            return;

        case "songPause":
            if (ttPlayer && ttPlayerReady) {
                try { ttPlayer.pauseVideo(); } catch (e) { /* player gone */ }
            }
            return;

        case "songResume":
            if (ttPlayer && ttPlayerReady) {
                try { ttPlayer.playVideo(); } catch (e) { /* player gone */ }
            }
            return;

        case "kick":
            document.location.href = "/track-timeline/lobbies";
            return;
    }

    if (message.startsWith("song:")) {
        let payload;
        try {
            payload = JSON.parse(message.substring("song:".length));
        } catch (e) {
            console.error("[TrackTimeline] bad song payload:", e);
            return;
        }
        ttEndChallengeWindow();
        playSong(payload.videoId, payload.startSeconds || 0, payload.endSeconds || 0);
        return;
    }

    if (message.startsWith("steal:")) {
        let payload;
        try {
            payload = JSON.parse(message.substring("steal:".length));
        } catch (e) {
            console.error("[TrackTimeline] bad steal payload:", e);
            return;
        }
        handleStealJoin(payload);
        refreshGame();
        return;
    }

    if (message.startsWith("stealTurn:")) {
        let payload;
        try {
            payload = JSON.parse(message.substring("stealTurn:".length));
        } catch (e) {
            console.error("[TrackTimeline] bad stealTurn payload:", e);
            return;
        }
        handleStealTurn(payload);
        refreshGame();
        return;
    }

    if (message.startsWith("challenge:")) {
        let payload;
        try {
            payload = JSON.parse(message.substring("challenge:".length));
        } catch (e) {
            console.error("[TrackTimeline] bad challenge payload:", e);
            return;
        }
        ttShowChallengeVote(payload);
        return;
    }

    if (message.startsWith("challengeEnd:")) {
        let payload;
        try {
            payload = JSON.parse(message.substring("challengeEnd:".length));
        } catch (e) {
            console.error("[TrackTimeline] bad challengeEnd payload:", e);
            return;
        }
        ttChallengeMyVote = { id: "", vote: "" };
        ttCloseChallengeModal();
        showStatus(payload.message);
        return;
    }

    if (message.startsWith("result:")) {
        let payload;
        try {
            payload = JSON.parse(message.substring("result:".length));
        } catch (e) {
            console.error("[TrackTimeline] bad result payload:", e);
            return;
        }
        ttCloseStealModal();
        ttCloseTurnTimerBanner();
        stopSong();
        showStatus(payload.bottomMessage);

        // Freeze the clock straight away: it belongs to a turn that has ended
        // and must not keep ticking behind the reveal.
        ttDeferTimerStart = true;

        const afterReveal = () => {
            ttDeferTimerStart = false;
            // A new round starts un-held, unlistened and with Play enabled:
            // the clip that was holding the clock belonged to the round that
            // just ended, and stopVideo does not reliably fire ENDED, so the
            // gates are cleared here rather than left to a state transition
            // that may never arrive. The turn clock stays off until this
            // round's clip ends or is paused — restartTurnTimer alone must
            // not start it here.
            ttTimerHeldForPlayback = false;
            ttTimerReleasedThisRound = false;
            ttClipReachedPlaying = false;
            ttClipListenedThisRound = false;
            ttClipFinished = false;
            ttPlaybackStartedThisRound = false;
            ttClearListenGate();
            refreshGame();
            if (payload.gameOver) {
                refreshControls();
            }
        };

        // Game-over with a configured Win Video: force the lobby to watch
        // instead of the click-away celebration popup.
        if (payload.gameOver && payload.winVideoId) {
            showWinVideoModal(payload, afterReveal);
            return;
        }

        showResultPopup(payload, afterReveal);
        return;
    }

    if (message.startsWith("status:")) {
        showStatus(message.substring("status:".length));
        refreshGame();
        return;
    }

    if (message.startsWith("alert:")) {
        showStatus(message.substring("alert:".length));
        return;
    }

    if (message.startsWith("lobbyMessage:")) {
        updateLobbyBanner(message.substring("lobbyMessage:".length));
        return;
    }

    if (message.startsWith("settings:")) {
        ttApplyLiveSettings(JSON.parse(message.substring("settings:".length)));
        return;
    }

    if (message.startsWith("turnTimer:")) {
        setTurnTimerSeconds(message.substring("turnTimer:".length));
        showStatus(ttTurnTimerSeconds > 0
            ? "Turn timer set to " + ttTurnTimerSeconds + "s"
            : "Turn timer turned off");
        // Apply a mid-game change immediately only when the clock is already
        // eligible to run (clip heard/paused). Otherwise just store the new
        // duration for the eventual release.
        if (ttTurnTimerSeconds <= 0) {
            ttCloseTurnTimerBanner();
            return;
        }
        if (!ttDeferTimerStart && !ttTimerHeldForPlayback && ttTimerReleasedThisRound) {
            doRestartTurnTimer();
        }
        return;
    }

    if (message.startsWith("chat:")) {
        addChatLine(message.substring("chat:".length));
        return;
    }

    // Anything unprefixed is a chat line.
    addChatLine(message);
}

// ------------------------------------------------------------ fragment refresh

// The lobby settings form is server-rendered once, so when anyone changes
// playback or song order the server sends the new values in a "settings:"
// message and every open page updates its own form to match.
function ttSyncLiveClipRow() {
    const mode = document.getElementById("tt-set-playback");
    const showClip = !mode || mode.value !== "full";
    const clip = document.getElementById("tt-set-clip");
    const label = document.getElementById("tt-set-clip-label");
    if (clip) clip.style.display = showClip ? "" : "none";
    if (label) label.style.display = showClip ? "" : "none";
}

function ttApplyLiveSettings(settings) {
    const mode = document.getElementById("tt-set-playback");
    const clip = document.getElementById("tt-set-clip");
    const fresh = document.getElementById("tt-set-fresh");
    if (mode) mode.value = settings.playbackMode;
    if (clip) clip.value = settings.clipSeconds;
    if (fresh) fresh.value = settings.freshSongsFirst ? "1" : "0";
    ttSyncLiveClipRow();
}

// ttSyncSelfStatus copies the board template's tokens/Buy into the header
// badge strip so those controls do not consume a row on every timeline.
function ttSyncSelfStatus() {
    const board = document.getElementById("tt-board");
    const dest = document.getElementById("tt-self-status");
    if (!board || !dest) return;
    const tpl = board.querySelector("#tt-self-status-template");
    if (!tpl) {
        dest.innerHTML = "";
        return;
    }
    dest.innerHTML = tpl.innerHTML;
    htmx.process(dest);
}

function refreshGame() {
    if (!ttLobbyId) return;
    const base = "/api/track-timeline/" + ttLobbyId;

    htmx.ajax("GET", base + "/current-card", { target: "#tt-current-card", swap: "innerHTML" });
    htmx.ajax("GET", base + "/decks", { target: "#tt-deck-info", swap: "innerHTML" });

    // The board is fetched rather than htmx.ajax'd so htmx.process can re-arm
    // the placement buttons in the new markup.
    const board = document.getElementById("tt-board");
    if (board) {
        fetch(base + "/timeline?t=" + Date.now(), { cache: "no-store" })
            .then((r) => r.text())
            .then((html) => {
                board.innerHTML = html;
                htmx.process(board);
                ttSyncSelfStatus();
                syncPlaybackUI();
                if (typeof roomPhoneSyncBoardVisibility === "function") {
                    roomPhoneSyncBoardVisibility();
                }
                restartTurnTimer();
            })
            .catch((e) => console.error("[TrackTimeline] board refresh failed:", e));
    }

    const pile = document.getElementById("tt-draw-pile-count");
    if (pile) {
        fetch(base + "/draw-pile", { cache: "no-store" })
            .then((r) => r.json())
            .then((info) => {
                pile.textContent = info.count;
                const stat = document.getElementById("tt-draw-pile-stat");
                if (stat) stat.title = info.tooltip;
            })
            .catch(() => {});
    }
}

// refreshControls re-fetches the page and swaps just the controls block, which
// changes shape between waiting, playing and finished.
function refreshControls() {
    if (!ttLobbyId) return;
    fetch("/track-timeline/" + ttLobbyId + "?t=" + Date.now(), { cache: "no-store" })
        .then((r) => r.text())
        .then((html) => {
            const parsed = new DOMParser().parseFromString(html, "text/html");
            const fresh = parsed.getElementById("tt-controls");
            const current = document.getElementById("tt-controls");
            if (fresh && current) {
                current.outerHTML = fresh.outerHTML;
                htmx.process(document.getElementById("tt-controls"));
            }
        })
        .catch((e) => console.error("[TrackTimeline] controls refresh failed:", e));
}

// -------------------------------------------------------------- youtube player

let ttPlayer = null;
let ttPlayerReady = false;
let ttPendingSong = null;

function loadYouTubeApi() {
    if (document.getElementById("youtube-iframe-api")) return;
    const script = document.createElement("script");
    script.id = "youtube-iframe-api";
    script.src = "https://www.youtube.com/iframe_api";
    document.head.appendChild(script);
}

// Called by the IFrame API once it has loaded.
window.onYouTubeIframeAPIReady = function () {
    ttPlayer = new YT.Player("tt-youtube-player", {
        height: "200",
        width: "200",
        playerVars: {
            controls: 0,
            disablekb: 1,
            fs: 0,
            modestbranding: 1,
            rel: 0,
            playsinline: 1,
        },
        events: {
            onReady: () => {
                ttPlayerReady = true;
                ttApplyVolumeToPlayer();
                if (ttPendingSong) {
                    const pending = ttPendingSong;
                    ttPendingSong = null;
                    playSong(pending.videoId, pending.startSeconds, pending.endSeconds);
                }
            },
            // 100 (gone), 101/150 (embedding disabled by the owner) and 2
            // (malformed id) are definitive: the video will never play here,
            // so the card is reported dead and swapped automatically for
            // free. This is the fallback for when the Data API check never
            // ran (no key, rate limited) or the video died since it last did
            // — the player's own browser is the oracle.
            //
            // Anything else (notably 5, an HTML5 player error) can be
            // transient, so it only surfaces a message and leaves the manual
            // paid skip as the way out.
            onError: (event) => {
                const fatal = [2, 100, 101, 150].includes(event.data);
                if (!fatal) {
                    showStatus("That video would not play. The player on turn can skip it.");
                    return;
                }
                showStatus("That video is unavailable — swapping it out.");
                ttReportDeadVideo();
            },
            // The authoritative source for every playback-driven UI bit
            // (spinning record, visualizer, the Play/Pause button's own
            // icon+label): driven off the player's actual state rather than
            // just the call sites that ask it to play/pause/stop, so a
            // buffered/ended video (or a fragment re-swap mid-playback) never
            // leaves the UI out of sync with what's actually audible.
            //
            // It is also where the held turn clock is released: the clip
            // reaching its endSeconds fires ENDED, and a manual pause fires
            // PAUSED, which are exactly the two moments the turn is meant to
            // start being timed.
            onStateChange: (event) => {
                if (event.data === YT.PlayerState.PLAYING) {
                    // Cue/load often emits PAUSED before the first PLAYING.
                    // Autoplays blocked by the browser do the same. Only a
                    // real PLAYING arms the end/pause release so the turn
                    // clock cannot start on those pre-play transitions.
                    ttClipReachedPlaying = true;
                    ttStartClipProgressTimer();
                }
                if (event.data === YT.PlayerState.PAUSED) {
                    ttStopClipProgressTimer();
                    ttUpdateClipProgress();
                }
                // ttClipReachedPlaying is false from stopSong until the next
                // clip really plays, so an ENDED that lags in from a song that
                // was just skipped or discarded is ignored here. Without that,
                // it marked the NEXT song finished and left Play disabled.
                if (event.data === YT.PlayerState.ENDED && ttClipReachedPlaying) {
                    // The clip ran its full length. Play must not silently
                    // restart it from the top after this — hearing it again
                    // is what the paid restart is for.
                    ttClipFinished = true;
                    ttStopClipProgressTimer();
                    ttFinishClipProgress();
                }
                if (ttClipReachedPlaying &&
                    (event.data === YT.PlayerState.ENDED || event.data === YT.PlayerState.PAUSED)) {
                    ttClipListenedThisRound = true;
                    ttReleaseTimerAfterPlayback();
                }
                syncPlaybackUI();
            },
        },
    });
};

// ttIsSpinning is whether the record should be turning right now: the clip is
// really playing, or the player is only briefly BUFFERING mid-clip (audio keeps
// coming from what is already buffered, and YouTube reports that as a state of
// its own, not PLAYING). Without the second case the record stopped on every
// rebuffer while the song carried on.
//
// The third case is the backstop for a state report that never arrives or
// reads wrong: if the playhead moved since the last look, audio is coming out,
// whatever getPlayerState says.
let ttLastSpinTime = -1;
let ttLastSpinMoved = 0;
function ttIsSpinning() {
    if (!ttPlayer || !ttPlayerReady) return false;
    try {
        const state = ttPlayer.getPlayerState();
        const now = ttPlayer.getCurrentTime();
        if (now !== ttLastSpinTime) {
            if (ttLastSpinTime >= 0 && now > ttLastSpinTime) ttLastSpinMoved = Date.now();
            ttLastSpinTime = now;
        }
        if (state === YT.PlayerState.PLAYING) return true;
        if (state === YT.PlayerState.BUFFERING && ttClipReachedPlaying && !ttClipFinished) return true;
        return !ttClipFinished && state !== YT.PlayerState.PAUSED && Date.now() - ttLastSpinMoved < 400;
    } catch (e) {
        return false;
    }
}

// ttApplySpin points the record, tonearm and visualizer at ttIsSpinning. Run
// from syncPlaybackUI AND on every clip-progress tick, so a state change or
// fragment swap that was missed or arrived out of order corrects itself within
// a tenth of a second instead of leaving the record stopped under live audio.
function ttApplySpin() {
    const spinning = ttIsSpinning();
    document.querySelectorAll(".tt-record").forEach((el) => {
        el.classList.toggle("is-spinning", spinning);
    });
    document.querySelectorAll(".tt-visualizer").forEach((el) => {
        el.classList.toggle("is-active", spinning);
    });
    document.querySelectorAll(".tt-tonearm").forEach((el) => {
        el.classList.toggle("is-active", spinning);
    });
}

// syncPlaybackUI re-applies the playing/paused state to every
// playback-reactive element on the page, based on the YouTube player's real
// current state. Needed both after a state change and after the current-card
// fragment is re-swapped (a refresh broadcast unrelated to playback
// re-renders these fresh, so they'd otherwise revert to their default
// not-playing look).
function syncPlaybackUI() {
    let playing = false;
    if (ttPlayer && ttPlayerReady) {
        try {
            playing = ttPlayer.getPlayerState() === YT.PlayerState.PLAYING;
        } catch (e) {
            // Nothing useful to do if the player went away mid-check.
        }
    }
    ttApplySpin();

    ttUpdateClipProgress();
    ttApplyVolumeUI();

    const btn = document.getElementById("tt-play-pause-btn");
    if (btn) {
        const icon = btn.querySelector(".bi");
        const label = btn.querySelector(".btn-label");
        if (icon) icon.className = playing ? "bi bi-pause-fill" : "bi bi-play-fill";
        if (label) label.textContent = playing ? "Pause" : "Play";
        // Once the clip has run its length there is nothing left to play or
        // resume: the only way to hear it again is the paid restart, so this
        // stops being a free second listen.
        btn.disabled = ttClipFinished && !playing;
        btn.title = btn.disabled
            ? "The clip has finished — use Restart to hear it again"
            : "Play or pause the song for everyone";
    }

    // The restart offer only makes sense once they've actually heard the clip
    // through or stopped it themselves. Driven off a flag rather than the
    // live player state so a fragment re-swap mid-round (someone else
    // guessing, a token changing) doesn't hide a button that had been earned.
    const replayBtn = document.getElementById("tt-replay-btn");
    if (replayBtn) {
        const noTokens = replayBtn.getAttribute("data-no-tokens") === "1";
        replayBtn.style.display = ttClipListenedThisRound ? "" : "none";
        replayBtn.disabled = noTokens || !ttClipListenedThisRound;
        if (noTokens) {
            replayBtn.title = "You need " + replayBtn.getAttribute("data-cost") + " tokens to restart.";
        } else if (!ttClipListenedThisRound) {
            replayBtn.title = "Hear the clip through (or pause it) before restarting.";
        } else {
            replayBtn.title = "Restart this clip from the beginning, once, for " + replayBtn.getAttribute("data-cost") + " tokens";
        }
    }

    // On offer from the moment the song has been started (same gate as Skip),
    // not only once the clip has been heard through: a different slice is just
    // as useful halfway through a clip that is not helping. Never used up --
    // every buy plays a fresh slice, so it stays on offer while they can pay.
    const newClipBtn = document.getElementById("tt-newclip-btn");
    if (newClipBtn) {
        const noTokens = newClipBtn.getAttribute("data-no-tokens") === "1";
        newClipBtn.style.display = ttPlaybackStartedThisRound ? "" : "none";
        newClipBtn.disabled = noTokens || !ttPlaybackStartedThisRound;
        if (noTokens) {
            newClipBtn.title = "You need " + newClipBtn.getAttribute("data-cost") + " tokens for a different clip.";
        } else if (!ttPlaybackStartedThisRound) {
            newClipBtn.title = "Play the song first.";
        } else {
            newClipBtn.title = "Hear a different part of this song for " + newClipBtn.getAttribute("data-cost") + " tokens";
        }
    }

    const skipBtn = document.getElementById("tt-skip-btn");
    if (skipBtn) {
        const noTokens = skipBtn.getAttribute("data-no-tokens") === "1";
        skipBtn.disabled = noTokens || !ttPlaybackStartedThisRound;
        if (noTokens) {
            skipBtn.title = "You need " + skipBtn.getAttribute("data-cost") + " tokens to skip.";
        } else if (!ttPlaybackStartedThisRound) {
            skipBtn.title = "Play the song first";
        } else {
            skipBtn.title = "Skip this song for " + skipBtn.getAttribute("data-cost") + " tokens and draw another";
        }
    }

    syncPlacementButtons();
    if (typeof roomPhoneSyncGuessYearBtn === "function") {
        roomPhoneSyncGuessYearBtn();
    }
}

// Place (+ drop zones and exact-year lock-in) stays off until Play has been
// clicked this round — same gate as Skip. Steal-turn drop zones are exempt:
// the song already played on the original turn, and stopSong clears the flag.
// Room phones gate the timeline behind "Guess year" (see roomEnterPlaceMode);
// once that screen is open the same listen-gate rules still apply here.
function syncPlacementButtons() {
    const exactYearOn = !!(document.getElementById("tt-use-exact-year") &&
        document.getElementById("tt-use-exact-year").checked);

    document.querySelectorAll("#tt-board .drop-zone").forEach((btn) => {
        const post = btn.getAttribute("hx-post") || "";
        const isPlace = post.indexOf("place-card") !== -1;
        if (!isPlace) {
            btn.disabled = false;
            btn.classList.remove("is-disabled");
            return;
        }
        const listenGateOpen = ttListenGateSatisfied();
        const blocked = !ttPlaybackStartedThisRound || exactYearOn || !listenGateOpen;
        btn.disabled = blocked;
        btn.classList.toggle("is-disabled", blocked);
        if (!ttPlaybackStartedThisRound) {
            btn.title = "Play the song first";
        } else if (!listenGateOpen) {
            btn.title = "Give everyone a chance to guess — " + ttListenGateRemainingSeconds() + "s left before you can place";
        } else if (exactYearOn) {
            btn.title = "Using exact-year wager — lock in above";
        } else {
            btn.title = "Place here";
        }
    });

    const useExact = document.getElementById("tt-use-exact-year");
    if (useExact) {
        // Template disables the checkbox (and marks the label) when tokens < 1.
        const tokenBlocked = !!(useExact.closest("label") &&
            useExact.closest("label").classList.contains("is-disabled"));
        if (!tokenBlocked) {
            useExact.disabled = !ttPlaybackStartedThisRound;
        }
    }

    ttValidateExactYear();
}

// ttPlayPauseClick is the turn player's single Play/Pause button. Which
// endpoint it hits depends on the player's own local playback state: nothing
// loaded yet starts the clip; paused resumes from exactly where it left off
// rather than restarting; playing pauses. Optimistic UI is deliberately not
// applied here -- the button waits for the resulting websocket broadcast
// (songPause/songResume/song:) to actually change anything, so it can never
// drift from what the other players see.
function ttPlayPauseClick() {
    if (!ttLobbyId) return;

    // Belt-and-braces with the disabled attribute syncPlaybackUI sets: once
    // the clip has played out, "play" would re-cue it from the top, which is
    // a free second listen and exactly what the paid restart exists to
    // charge for.
    if (ttClipFinished) return;

    let state = -1;
    if (ttPlayer && ttPlayerReady) {
        try { state = ttPlayer.getPlayerState(); } catch (e) { /* player gone */ }
    }

    const action = state === YT.PlayerState.PLAYING ? "pause-song"
        : state === YT.PlayerState.PAUSED ? "resume-song"
        : "play-song";

    fetch("/api/track-timeline/" + ttLobbyId + "/" + action, { method: "POST" })
        .catch((e) => console.error("[TrackTimeline] " + action + " failed:", e));
}

function ttAlert(message) {
    const backdrop = document.createElement("div");
    backdrop.className = "tt-popup-backdrop";

    const popup = document.createElement("div");
    popup.className = "tt-popup";

    const text = document.createElement("div");
    text.className = "tt-popup-artist wrap-new-lines";
    text.textContent = message;
    popup.appendChild(text);

    const actions = document.createElement("div");
    actions.className = "tt-confirm-actions";

    const ok = document.createElement("button");
    ok.type = "button";
    ok.className = "btn-small";
    ok.textContent = "OK";
    ok.addEventListener("click", () => backdrop.remove());
    actions.appendChild(ok);
    popup.appendChild(actions);
    backdrop.appendChild(popup);
    document.body.appendChild(backdrop);
}

function ttStartGame() {
    if (!ttLobbyId) return;
    ttPostStart();
}

function ttPostStart() {
    fetch("/api/track-timeline/" + ttLobbyId + "/start", {
        method: "POST",
    })
        .then((response) => response.text().then((text) => ({ status: response.status, text: text })))
        .then((result) => {
            if (result.status !== 200) {
                showStatus(result.text);
                return;
            }
            const text = (result.text || "").trim();
            if (text && text !== "Game started.") {
                ttAlert(text);
            }
        })
        .catch((e) => console.error("[TrackTimeline] start failed:", e));
}

// ttReportDeadVideo asks the server to bin the current song and draw another.
// Every client's player raises the same error, but the server only accepts
// this from the player on turn, so the rest get a harmless rejection rather
// than a pile-up of skips. Fire-and-forget: the resulting refresh/status
// broadcast is what actually updates everyone.
function ttReportDeadVideo() {
    if (!ttLobbyId) return;
    fetch("/api/track-timeline/" + ttLobbyId + "/dead-video", { method: "POST" })
        .catch(() => {});
}

function playSong(videoId, startSeconds, endSeconds) {
    if (!videoId) return;

    ttClipStartSeconds = startSeconds || 0;
    ttClipEndSeconds = endSeconds || 0;
    ttClipDurationSeconds = (endSeconds > startSeconds) ? (endSeconds - startSeconds) : 0;
    ttResetClipProgress();

    // The API may not have finished loading when the first song arrives; hold
    // it and play once the player reports ready.
    if (!ttPlayerReady || !ttPlayer) {
        ttPendingSong = { videoId: videoId, startSeconds: startSeconds, endSeconds: endSeconds };
        return;
    }

    // A fresh clip means the turn hasn't started being timed yet -- the clock
    // is held until this finishes or the player pauses it (see
    // ttHoldTimerForPlayback).
    ttHoldTimerForPlayback();
    ttPlaybackStartedThisRound = true;
    // Explicit, not just relying on ttHoldTimerForPlayback's own reset: a
    // paid Restart (ReplaySong) calls playSong() again for the exact same
    // round, and both of these gate real UI actions (the Play/Pause click
    // handler and the clip-progress ring) on being accurate right now, not
    // eventually once some event fires.
    ttClipFinished = false;
    ttClipReachedPlaying = false;
    // Only arm the listen gate on this round's first play. A paid Restart is
    // a second listen, not a second decision window -- the player already
    // earned placement eligibility once this round, and restarting the clip
    // must not take it back. ttListenGateDeadlineMs stays nonzero from the
    // first arm until ttClearListenGate() runs at the round's actual end, so
    // this naturally re-arms only for a genuinely new round.
    if (ttListenGateDeadlineMs === 0) {
        ttStartListenGate();
    }

    // Re-apply the buttons now rather than waiting for the player's PLAYING
    // event: clearing ttClipFinished above is what re-enables Play after a
    // Restart or New Clip, and if the video is slow to start (or autoplay is
    // blocked) that event may be a long time coming, leaving Play disabled
    // with nothing playing.
    syncPlaybackUI();

    try {
        // endSeconds is the IFrame API's own clip support: it stops there and
        // fires ENDED, which is what releases the turn timer. 0/undefined
        // means play to the end of the video ('full' playback mode).
        const request = { videoId: videoId, startSeconds: startSeconds || 0 };
        if (endSeconds > 0) request.endSeconds = endSeconds;
        ttPlayer.loadVideoById(request);
        ttPlayer.playVideo();
    } catch (e) {
        console.error("[TrackTimeline] playback failed:", e);
        return;
    }

    // Browsers block audio until the page has been interacted with, and a
    // player who has only just loaded the lobby may not have clicked anything.
    // Detect the silent failure and offer a button, since a gesture is the only
    // thing that can start it.
    setTimeout(() => {
        try {
            if (ttPlayer.getPlayerState() !== YT.PlayerState.PLAYING) {
                showAudioUnlock();
            } else {
                hideAudioUnlock();
            }
        } catch (e) {
            // getPlayerState throws if the player went away mid-check.
        }
    }, 1200);
}

function stopSong() {
    // Clear the hold gate BEFORE stopVideo so a lagged ENDED/PAUSED from the
    // forced stop cannot release and flash the turn timer as the round ends.
    // ttTimerReleasedThisRound must clear too, not just the hold flag: a
    // round that ends without a reveal (timeout discard, skip) never runs
    // afterReveal's reset, so a released flag from a clip heard earlier in
    // this same turn would otherwise survive into the next song/turn and let
    // restartTurnTimer start the clock again before anyone has pressed Play.
    ttTimerHeldForPlayback = false;
    ttTimerReleasedThisRound = false;
    ttClipReachedPlaying = false;
    // An explicit stop is not a playhead that merely stopped moving: drop the
    // movement backstop so the record halts now, not up to 400ms later.
    ttLastSpinMoved = 0;
    // A round that ends here without a reveal never reaches the "result:"
    // handler's own ttCloseTurnTimerBanner() call, so a banner already shown
    // for the round that just got discarded (e.g. "ran out of time") would
    // otherwise linger on screen, frozen, into the next player's turn.
    ttCloseTurnTimerBanner();
    ttTurnTimerDeadlineMs = 0;

    ttStopClipProgressTimer();
    ttResetClipProgress();

    if (ttPlayer && ttPlayerReady) {
        try {
            ttPlayer.stopVideo();
        } catch (e) {
            // Nothing useful to do if the player is already gone.
        }
    }
    ttPendingSong = null;
    hideAudioUnlock();

    // songStop means this song is done with — either the round is ending, or
    // it was skipped/replaced and a different song has been drawn. Either way
    // the next one has to be played before it can be skipped or restarted in
    // turn, so the per-song gates reset here as well as on the reveal.
    ttPlaybackStartedThisRound = false;
    ttClipListenedThisRound = false;
    ttClipFinished = false;
    ttClearListenGate();

    // stopVideo's own onStateChange event can lag by a beat; update the UI
    // immediately rather than waiting on it.
    syncPlaybackUI();
}

function showAudioUnlock() {
    const unlock = document.getElementById("tt-audio-unlock");
    if (unlock) unlock.style.display = "block";
}

function hideAudioUnlock() {
    const unlock = document.getElementById("tt-audio-unlock");
    if (unlock) unlock.style.display = "none";
}

// Wired to the unlock button; runs inside a real user gesture.
function ttUnlockAudio() {
    if (ttPlayer && ttPlayerReady) {
        try {
            ttPlayer.playVideo();
        } catch (e) {
            console.error("[TrackTimeline] unlock failed:", e);
        }
    }
    hideAudioUnlock();
}

// ------------------------------------------------------------------- timer

// The turn clock does not run while the clip is playing. A player shouldn't
// be spending their thinking time listening — the timer is for deciding where
// the song goes, so it starts once the clip finishes on its own or the player
// pauses it, whichever comes first. ttHoldTimerForPlayback stops and holds it;
// ttReleaseTimerAfterPlayback starts it (idempotent — the release fires from
// both the ENDED and PAUSED transitions, and repeated calls after the first
// are no-ops rather than restarts, so pausing a second time doesn't hand the
// player a fresh full clock).
//
// Display matches the steal countdown: a fixed top-of-screen banner with a
// large bare number, driven by a deadline so board refreshes (guesses, token
// changes) do not reset the remaining time. Unlike steal, the deadline is
// client-local — the server has no turn-timer authority of its own.
let ttTimerHeldForPlayback = false;
let ttTimerReleasedThisRound = false;
let ttClipReachedPlaying = false;
let ttTurnTimerInterval = null;
let ttTurnTimerDeadlineMs = 0;

// ttClipListenedThisRound gates the paid restart button: it appears only once
// the clip has been heard through or deliberately paused, never before the
// first listen. ttClipFinished is the narrower "ran to its end" case, which
// additionally disables Play so it can't be used as a free second listen —
// pausing leaves Play enabled, because resuming a pause is not re-hearing it.
let ttClipListenedThisRound = false;
let ttClipFinished = false;

// ttPlaybackStartedThisRound gates Skip and the turn player's place buttons:
// you can only skip or place a song you have actually tried to play, not shop
// for an easier one sight-unseen. Set when the song is cued rather than when
// playback succeeds, so a video that errors out still leaves Skip available —
// that is precisely when it is needed, and only the definitively-dead error
// codes get auto-skipped for free.
let ttPlaybackStartedThisRound = false;

// The listen gate holds placement (not guessing) back for a flat
// TT_MIN_LISTEN_MS after Play is pressed, so a fast placement by the turn
// player cannot end the round -- and close everyone else's guessing window
// (SubmitGuess refuses once the phase reaches reveal) -- before the rest of
// the lobby has had a real chance to type a guess. Deliberately a flat
// duration rather than a fraction of the clip: it is measuring real time to
// think/type, not clip length, so it applies the same way even to a clip
// shorter than TT_MIN_LISTEN_MS.
//
// Client-side only, same trust model this file already uses for "Play the
// song first" (ttPlaybackStartedThisRound has no server-side check either):
// a good-faith courtesy, not an anti-cheat boundary.
const TT_MIN_LISTEN_MS = 20000;
let ttListenGateDeadlineMs = 0;
let ttListenGateInterval = null;

function ttStartListenGate() {
    ttListenGateDeadlineMs = Date.now() + TT_MIN_LISTEN_MS;
    if (ttListenGateInterval) clearInterval(ttListenGateInterval);
    ttListenGateInterval = setInterval(() => {
        if (Date.now() >= ttListenGateDeadlineMs && ttListenGateInterval) {
            clearInterval(ttListenGateInterval);
            ttListenGateInterval = null;
        }
        syncPlacementButtons();
        if (typeof roomPhoneSyncGuessYearBtn === "function") {
            roomPhoneSyncGuessYearBtn();
        }
    }, 500);
}

function ttClearListenGate() {
    ttListenGateDeadlineMs = 0;
    if (ttListenGateInterval) {
        clearInterval(ttListenGateInterval);
        ttListenGateInterval = null;
    }
}

function ttListenGateSatisfied() {
    return ttListenGateDeadlineMs !== 0 && Date.now() >= ttListenGateDeadlineMs;
}

function ttListenGateRemainingSeconds() {
    return Math.max(0, Math.ceil((ttListenGateDeadlineMs - Date.now()) / 1000));
}

// setTurnTimerSeconds keeps the tracked duration and the header badge's
// visibility in sync. The badge element is always in the DOM (even when the
// lobby loaded with the timer off) so a mid-game enable can show it without
// a reload — same approach timeline-trivia uses.
function setTurnTimerSeconds(seconds) {
    ttTurnTimerSeconds = parseInt(seconds, 10) || 0;
    const statEl = document.getElementById("tt-turn-timer-stat");
    if (statEl) {
        statEl.style.display = ttTurnTimerSeconds > 0 ? "" : "none";
    }
    if (ttTurnTimerSeconds <= 0) {
        const timerEl = document.getElementById("tt-turn-timer");
        if (timerEl) timerEl.textContent = "";
    }
}

function ttCloseTurnTimerBanner() {
    if (ttTurnTimerInterval) {
        clearInterval(ttTurnTimerInterval);
        ttTurnTimerInterval = null;
    }
    const existing = document.getElementById("tt-turn-timer-modal");
    if (existing) existing.remove();

    // The header badge is only ever written by the banner's own tick, so
    // without this it keeps showing whatever number the ordinary countdown
    // last wrote — frozen and increasingly wrong — all the way through a
    // steal window/turn (which suppress the ordinary timer entirely) and
    // into the next round, until a fresh ordinary countdown happens to
    // start and overwrite it again.
    const badgeEl = document.getElementById("tt-turn-timer");
    if (badgeEl) badgeEl.textContent = "";
}

function ttHoldTimerForPlayback() {
    ttTimerHeldForPlayback = true;
    ttTimerReleasedThisRound = false;
    ttClipReachedPlaying = false;
    ttClipListenedThisRound = false;
    ttClipFinished = false;
    ttCloseTurnTimerBanner();
    ttTurnTimerDeadlineMs = 0;
}

function ttReleaseTimerAfterPlayback() {
    if (!ttTimerHeldForPlayback || ttTimerReleasedThisRound) return;
    if (ttDeferTimerStart) return;
    ttTimerHeldForPlayback = false;
    ttTimerReleasedThisRound = true;
    doRestartTurnTimer();
}

function restartTurnTimer() {
    if (ttDeferTimerStart) return;
    // A board refresh mid-clip must not sneak the clock back on behind the
    // song that is still playing.
    if (ttTimerHeldForPlayback) return;
    // The clock only becomes eligible after clip end/pause. Board swaps and
    // the post-reveal refresh must not start it early.
    if (!ttTimerReleasedThisRound) return;
    // Already ticking — keep the existing deadline (refresh must not reset).
    if (ttTurnTimerInterval) return;
    doRestartTurnTimer();
}

function doRestartTurnTimer() {
    if (ttTurnTimerSeconds <= 0) {
        ttCloseTurnTimerBanner();
        ttTurnTimerDeadlineMs = 0;
        return;
    }

    // Whose turn it is comes from the DOM rather than from JS state, so a
    // refreshed board is always the source of truth.
    const currentCard = document.querySelector("#tt-board .player-card.is-current");
    if (!currentCard) {
        ttCloseTurnTimerBanner();
        ttTurnTimerDeadlineMs = 0;
        return;
    }

    const deadlineMs = Date.now() + ttTurnTimerSeconds * 1000;
    ttShowTurnTimerBanner(deadlineMs);
}

// ttShowTurnTimerBanner mirrors the steal attempt banner: non-blocking so the
// board's placement buttons stay clickable, large centered countdown, short
// instructional heading. Everyone sees the same clock; only the player on
// turn reports the timeout when it hits zero.
function ttShowTurnTimerBanner(deadlineMs) {
    ttCloseTurnTimerBanner();
    if (ttTurnTimerSeconds <= 0 || !deadlineMs) return;
    ttTurnTimerDeadlineMs = deadlineMs;

    const currentCard = document.querySelector("#tt-board .player-card.is-current");
    if (!currentCard) return;
    const mine = currentCard.classList.contains("is-me");
    const nameEl = currentCard.querySelector(".player-name");
    const name = nameEl ? nameEl.textContent.trim() : "Someone";

    const wrap = document.createElement("div");
    wrap.id = "tt-turn-timer-modal";
    wrap.className = "tt-steal-banner-wrap";

    const popup = document.createElement("div");
    popup.className = "tt-steal-banner";

    const heading = document.createElement("div");
    heading.className = "tt-popup-title";
    heading.textContent = mine ? "Your turn!" : (name + "'s turn");
    popup.appendChild(heading);

    if (mine) {
        const hint = document.createElement("div");
        hint.className = "tt-popup-artist";
        hint.textContent = "Place it on your timeline before time runs out.";
        popup.appendChild(hint);
    }

    const ringObj = ttCreateCountdownRing();
    popup.appendChild(ringObj.element);

    wrap.appendChild(popup);
    document.body.appendChild(wrap);

    const badgeEl = document.getElementById("tt-turn-timer");
    let expired = false;

    const totalTurnMs = (ttTurnTimerSeconds || 30) * 1000;
    const tick = () => {
        const remainingMs = ttTurnTimerDeadlineMs - Date.now();
        ringObj.update(remainingMs, totalTurnMs);
        const seconds = Math.max(0, Math.ceil(remainingMs / 1000));
        if (badgeEl) badgeEl.textContent = seconds + "s";

        if (remainingMs <= 0) {
            clearInterval(ttTurnTimerInterval);
            ttTurnTimerInterval = null;
            if (expired) return;
            expired = true;
            // Only the player whose turn it is reports the timeout; the server
            // re-checks anyway, so a stale call cannot end somebody else's turn.
            if (mine && ttLobbyId) {
                fetch("/api/track-timeline/" + ttLobbyId + "/timeout", { method: "POST" })
                    .catch(() => {});
            }
        }
    };
    tick();
    if (!expired) {
        ttTurnTimerInterval = setInterval(tick, 200);
    }
}

// ------------------------------------------------------------------ display

// ------------------------------------------------------------ guess drafts
//
// A guess is judged when the round ends, not when it is typed, and a round can
// outlive the song it started with: a skip moves everyone on to a new song but
// keeps the guesses made on the old one. So the boxes are saved to the server as
// they are typed, tagged with the song they were typed for. Whatever is in them
// when a song is skipped or a placement locks in then counts as the player's
// guess for that song, whether or not they got as far as pressing Guess, and a
// guess that reaches the server a beat after the skip is still filed against the
// song it was for.

let ttGuessCardId = "";
let ttGuessDirty = false;
let ttGuessTimer = null;

function ttGuessBox(name) {
    return document.querySelector('#tt-current-card input[name="' + name + '"]');
}

function ttGuessBoxes() {
    return { title: ttGuessBox("guessTitle"), artist: ttGuessBox("guessArtist") };
}

function ttGuessCurrentCardId() {
    const marker = document.getElementById("tt-guess-card");
    return marker ? marker.dataset.cardId || "" : "";
}

// Saves the boxes as a draft guess for cardId. Resolves true if the server kept it.
function ttSendGuessDraft(cardId, title, artist) {
    if (!ttLobbyId || !cardId) return Promise.resolve(false);
    const body = new URLSearchParams({ cardId: cardId, guessTitle: title, guessArtist: artist });
    return fetch("/api/track-timeline/" + ttLobbyId + "/guess-draft", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: body.toString(),
        keepalive: true,
    }).then((response) => response.ok).catch(() => false);
}

// Sends the boxes now if anything has changed since they were last saved.
function ttFlushGuessDraft() {
    if (ttGuessTimer) {
        clearTimeout(ttGuessTimer);
        ttGuessTimer = null;
    }
    if (!ttGuessDirty) return Promise.resolve(false);
    const boxes = ttGuessBoxes();
    if (!boxes.title || !boxes.artist) return Promise.resolve(false);
    ttGuessDirty = false;
    return ttSendGuessDraft(ttGuessCardId, boxes.title.value.trim(), boxes.artist.value.trim());
}

// Called whenever the current-card fragment is swapped. If the song under the
// boxes has changed, the boxes are saved against the song they were typed for and
// then emptied: they are hx-preserve'd, so otherwise last song's text would sit
// in them as if it were a guess for the new one.
function ttSyncGuessCard() {
    const now = ttGuessCurrentCardId();
    if (!now || now === ttGuessCardId) return;

    const previous = ttGuessCardId;
    ttGuessCardId = now;
    if (!previous) return;

    const boxes = ttGuessBoxes();
    if (ttGuessTimer) {
        clearTimeout(ttGuessTimer);
        ttGuessTimer = null;
    }
    if (boxes.title && boxes.artist) {
        const title = boxes.title.value.trim();
        const artist = boxes.artist.value.trim();
        if (title || artist) {
            ttSendGuessDraft(previous, title, artist).then((kept) => {
                // The server only keeps it when that song still belongs to the
                // round in progress, i.e. it was skipped rather than finished.
                if (kept) showStatus("Your guess was kept for the song that was skipped.");
            });
        }
        boxes.title.value = "";
        boxes.artist.value = "";
    }
    ttGuessDirty = false;
}

document.addEventListener("input", (event) => {
    const name = event.target && event.target.name;
    if (name !== "guessTitle" && name !== "guessArtist") return;
    if (!event.target.closest("#tt-current-card")) return;

    // The song the player is typing for is the one the page last rendered.
    if (!ttGuessCardId) ttGuessCardId = ttGuessCurrentCardId();
    ttGuessDirty = true;
    if (ttGuessTimer) clearTimeout(ttGuessTimer);
    ttGuessTimer = setTimeout(ttFlushGuessDraft, TT_GUESS_DRAFT_DELAY_MS);
});

// A tab being closed or backgrounded is the last chance to save what is typed.
document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") ttFlushGuessDraft();
});
window.addEventListener("pagehide", () => { ttFlushGuessDraft(); });

function showStatus(message) {
    const el = document.getElementById("tt-message");
    if (!el || !message) return;

    el.textContent = message;
    if (ttStatusTimeout) clearTimeout(ttStatusTimeout);
    ttStatusTimeout = setTimeout(() => { el.textContent = ""; }, TT_STATUS_MESSAGE_MS);
}

function updateLobbyBanner(message) {
    const banner = document.getElementById("tt-lobby-message");
    if (!banner) return;
    banner.textContent = message;
    banner.style.display = message ? "block" : "none";
}

// gsChat.append renders with innerHTML so it can apply the <blue>/<green>/<red>
// colour tokens. Anything the server interpolates into a chat line is escaped
// server-side before it reaches here.
function addChatLine(line) {
    const messages = document.getElementById("chat-messages");
    if (!messages) return;
    if (typeof gsChat !== "undefined") {
        gsChat.append(messages, line);
    }
}

// showResultPopup builds its content with textContent throughout: song titles,
// artist names and player names are all user-authored, and this is the one
// place they are rendered outside a Go template's escaping.
function showResultPopup(payload, onDone) {
    const backdrop = document.createElement("div");
    backdrop.className = "tt-popup-backdrop";

    const popup = document.createElement("div");
    popup.className = "tt-popup " + (payload.type === "won" ? "is-won" : "is-discarded");

    const year = document.createElement("div");
    year.className = "tt-popup-year";
    year.textContent = payload.releaseYear;
    popup.appendChild(year);

    const artist = document.createElement("div");
    artist.className = "tt-popup-artist";
    artist.textContent = payload.artist;
    popup.appendChild(artist);

    const title = document.createElement("div");
    title.className = "tt-popup-title";
    title.textContent = "“" + payload.title + "”";
    popup.appendChild(title);

    const verdict = document.createElement("div");
    verdict.className = "tt-popup-verdict";
    if (payload.winnerName) {
        verdict.textContent = payload.wonByChallenge
            ? payload.winnerName + " stole it with a challenge"
            : payload.winnerName + " placed it correctly";
    } else {
        verdict.textContent = "Nobody placed it correctly";
    }
    popup.appendChild(verdict);

    // Every right part of a guess earns its own tokens -- no single-token
    // race -- so there can be more than one of these, one line per player.
    (payload.guessTokenWinners || []).forEach((winner) => {
        const guessLine = document.createElement("div");
        guessLine.className = "tt-popup-guess";
        const quoted = winner.guessText ? " — “" + winner.guessText + "”" : "";
        guessLine.textContent = winner.name + " named it" + quoted + " — won " + winner.tokens + (winner.tokens === 1 ? " token" : " tokens");
        popup.appendChild(guessLine);
    });

    // "won" carries a win-celebration; "discarded" carries the turn player's
    // lose-celebration — same split as timeline-trivia's correct/incorrect.
    // Game-over reuses type "won" with the game winner's celebration stamped
    // on top.
    const isCelebratable = payload.type === "won" || payload.type === "discarded";
    const hasCelebration = isCelebratable && (payload.hasGif || payload.celebration);
    if (payload.hasGif && payload.userId) {
        const gif = document.createElement("img");
        gif.className = "tt-popup-gif";
        gif.alt = "";
        const gifRoute = payload.type === "won" ? "win-gif" : "lose-gif";
        gif.src = "/api/user/" + encodeURIComponent(payload.userId) + "/" + gifRoute;
        popup.appendChild(gif);
    }
    if (isCelebratable && payload.celebration) {
        const celebration = document.createElement("div");
        celebration.className = "tt-popup-celebration";
        celebration.textContent = payload.celebration;
        popup.appendChild(celebration);
    }

    if (payload.nextPlayerName) {
        const next = document.createElement("div");
        next.className = "tt-popup-next";
        next.textContent = "Next: " + payload.nextPlayerName;
        popup.appendChild(next);
    }

    backdrop.appendChild(popup);
    document.body.appendChild(backdrop);

    let finished = false;
    const finish = () => {
        if (finished) return;
        finished = true;
        backdrop.remove();
        if (onDone) onDone();
    };

    backdrop.addEventListener("click", finish);
    let dismissAfter = 4000;
    if (payload.gameOver) dismissAfter = 6000;
    else if (hasCelebration) dismissAfter = 5000;
    setTimeout(finish, dismissAfter);
}

// showWinVideoModal forces the lobby to watch the game winner's account Win
// Video. Unlike showResultPopup, clicks do nothing — the overlay only clears
// when YouTube reports ENDED (with a long safety timeout if the API stalls).
let ttWinVideoPlayer = null;

function showWinVideoModal(payload, onDone) {
    const existing = document.querySelector(".tt-win-video-backdrop");
    if (existing) existing.remove();
    if (ttWinVideoPlayer) {
        try { ttWinVideoPlayer.destroy(); } catch (e) { /* ignore */ }
        ttWinVideoPlayer = null;
    }

    const backdrop = document.createElement("div");
    backdrop.className = "tt-popup-backdrop tt-win-video-backdrop";
    // Block interaction with the board; deliberately no click-to-dismiss.
    backdrop.style.cursor = "default";

    const popup = document.createElement("div");
    popup.className = "tt-popup tt-win-video-popup";

    // Frame wrap + click shield: YouTube has no "can't pause" API, so hide
    // controls and eat pointer events on the iframe. PAUSED still force-
    // resumes below in case something else interrupts playback.
    const frameWrap = document.createElement("div");
    frameWrap.className = "tt-win-video-frame-wrap";
    const playerHost = document.createElement("div");
    playerHost.id = "tt-win-video-player";
    playerHost.className = "tt-win-video-frame";
    frameWrap.appendChild(playerHost);
    const clickShield = document.createElement("div");
    clickShield.className = "tt-win-video-clickshield";
    clickShield.setAttribute("aria-hidden", "true");
    frameWrap.appendChild(clickShield);
    popup.appendChild(frameWrap);

    const caption = document.createElement("div");
    caption.className = "tt-win-video-caption";
    const winnerLabel = payload.winnerName || "Someone";
    caption.textContent = winnerLabel + " won";
    popup.appendChild(caption);

    // Autoplay is often blocked until a gesture; the clickshield would then
    // trap the lobby until the safety timeout. Offer a tap-to-start that
    // only unlocks playback — never dismisses.
    const tapHint = document.createElement("button");
    tapHint.type = "button";
    tapHint.className = "tt-win-video-tap";
    tapHint.textContent = "Tap to play";
    tapHint.style.display = "none";
    popup.appendChild(tapHint);

    backdrop.appendChild(popup);
    document.body.appendChild(backdrop);

    let finished = false;
    let hasReachedPlaying = false;
    const finish = () => {
        if (finished) return;
        finished = true;
        if (ttWinVideoSafety) {
            clearTimeout(ttWinVideoSafety);
            ttWinVideoSafety = null;
        }
        if (ttWinVideoPlayer) {
            try { ttWinVideoPlayer.destroy(); } catch (e) { /* ignore */ }
            ttWinVideoPlayer = null;
        }
        backdrop.remove();
        if (onDone) onDone();
    };

    // Safety net if ENDED never arrives (blocked embed, network stall).
    let ttWinVideoSafety = setTimeout(finish, 3 * 60 * 1000);

    const startSeconds = payload.winVideoStartSeconds || 0;
    const videoId = payload.winVideoId;

    const tryPlay = () => {
        if (!ttWinVideoPlayer) return;
        try {
            ttWinVideoPlayer.seekTo(startSeconds, true);
            ttWinVideoPlayer.playVideo();
        } catch (e) { /* ignore */ }
    };

    tapHint.addEventListener("click", (e) => {
        e.stopPropagation();
        tryPlay();
    });

    const mountPlayer = () => {
        if (finished) return;
        if (typeof YT === "undefined" || !YT.Player) {
            // IFrame API not ready — fall back rather than trap the lobby.
            finish();
            return;
        }
        if (ttWinVideoPlayer) return;
        ttWinVideoPlayer = new YT.Player("tt-win-video-player", {
            width: "640",
            height: "360",
            videoId: videoId,
            playerVars: {
                autoplay: 1,
                start: startSeconds,
                controls: 0,
                disablekb: 1,
                fs: 0,
                rel: 0,
                modestbranding: 1,
                playsinline: 1,
            },
            events: {
                onReady: (event) => {
                    try {
                        event.target.seekTo(startSeconds, true);
                        event.target.playVideo();
                    } catch (e) { /* ignore */ }
                    // If autoplay is blocked, surface the tap hint shortly.
                    setTimeout(() => {
                        if (!finished && !hasReachedPlaying) {
                            tapHint.style.display = "";
                        }
                    }, 1500);
                },
                onStateChange: (event) => {
                    if (finished) return;
                    if (event.data === YT.PlayerState.PLAYING) {
                        hasReachedPlaying = true;
                        tapHint.style.display = "none";
                        return;
                    }
                    if (event.data === YT.PlayerState.ENDED) {
                        finish();
                        return;
                    }
                    // No true unpausable mode — resume once playback has begun.
                    if (hasReachedPlaying && event.data === YT.PlayerState.PAUSED) {
                        try { event.target.playVideo(); } catch (e) { /* ignore */ }
                    }
                },
                onError: () => {
                    finish();
                },
            },
        });
    };

    loadYouTubeApi();
    if (typeof YT !== "undefined" && YT.Player) {
        mountPlayer();
    } else {
        const prev = window.onYouTubeIframeAPIReady;
        window.onYouTubeIframeAPIReady = function () {
            if (typeof prev === "function") prev();
            mountPlayer();
        };
    }
}

// -------------------------------------------------------------- steal modal
//
// Unlike the turn timer (each client starts its own independent local
// countdown on receipt), the steal countdown's length is server-authoritative:
// the payload carries the time left (remainingMs), and each client counts
// down from receipt using only its own clock's elapsed time — never an
// absolute server timestamp, which a skewed client clock would misread. The
// server enforces the deadline itself (a scheduled
// time.AfterFunc) regardless of what any client does — this modal is display
// only, never the actual authority on when the phase ends.

let ttStealInterval = null;

function ttCloseStealModal() {
    if (ttStealInterval) {
        clearInterval(ttStealInterval);
        ttStealInterval = null;
    }
    const existing = document.getElementById("tt-steal-modal");
    if (existing) existing.remove();
}

// ttShowStealModal builds the countdown modal. opts: heading, hint (may be
// empty), deadlineMs, showJoinButton, blocking. blocking defaults to true
// (a full-screen backdrop, for spectators with nothing to click); pass false
// for the active stealer's own turn, so the board's own +-slot buttons
// underneath — already wired to their steal attempt — stay clickable instead
// of being covered.
function ttShowStealModal(opts) {
    ttCloseStealModal();

    // Pause the ordinary turn timer while this is up, the same way the
    // reveal popup already does — it belongs to a phase that is not "waiting
    // on the turn player's own timer" anymore.
    ttDeferTimerStart = true;
    ttCloseTurnTimerBanner();
    ttTurnTimerDeadlineMs = 0;

    const blocking = opts.blocking !== false;

    const backdrop = document.createElement("div");
    backdrop.id = "tt-steal-modal";
    backdrop.className = blocking ? "tt-popup-backdrop" : "tt-steal-banner-wrap";

    const popup = document.createElement("div");
    popup.className = blocking ? "tt-popup tt-steal-popup" : "tt-steal-banner";

    const heading = document.createElement("div");
    heading.className = "tt-popup-title";
    heading.textContent = opts.heading;
    popup.appendChild(heading);

    if (opts.hint) {
        const hint = document.createElement("div");
        hint.className = "tt-popup-artist";
        hint.textContent = opts.hint;
        popup.appendChild(hint);
    }

    const ringObj = ttCreateCountdownRing();
    popup.appendChild(ringObj.element);

    let joinButton = null;
    if (opts.showJoinButton) {
        joinButton = document.createElement("button");
        joinButton.type = "button";
        joinButton.textContent = "Steal";
        joinButton.className = "btn-small tt-steal-join-button";
        // Disabled until the modal has actually mounted (enabled below via
        // requestAnimationFrame), so a click cannot land before the window
        // has genuinely opened on this client. There is only one steal
        // attempt per round now (a race, first click wins) — a rejection here
        // just means someone else already claimed it, not that this player's
        // own claim is pending.
        joinButton.disabled = true;
        joinButton.addEventListener("click", () => {
            if (joinButton.disabled) return;
            joinButton.disabled = true;
            joinButton.textContent = "Claiming...";
            fetch("/api/track-timeline/" + ttLobbyId + "/claim-steal", { method: "POST" })
                .then((r) => r.text())
                .then((text) => {
                    if (joinButton.textContent !== "Claiming...") return;
                    joinButton.textContent = text;
                })
                .catch((e) => console.error("[TrackTimeline] claim-steal failed:", e));
        });
        popup.appendChild(joinButton);
    }

    backdrop.appendChild(popup);
    document.body.appendChild(backdrop);

    if (joinButton) {
        requestAnimationFrame(() => {
            joinButton.disabled = false;
        });
    }

    const totalStealMs = Math.max(1000, opts.deadlineMs - Date.now());
    const tick = () => {
        const remainingMs = opts.deadlineMs - Date.now();
        if (remainingMs <= 0) {
            ringObj.update(0, totalStealMs);
            if (joinButton) {
                joinButton.disabled = true;
                joinButton.textContent = "Time's up";
            }
            clearInterval(ttStealInterval);
            ttStealInterval = null;
            return;
        }
        ringObj.update(remainingMs, totalStealMs);
    };
    tick();
    ttStealInterval = setInterval(tick, 100);
}

// ttLocalDeadline turns a steal payload into a deadline on THIS browser's
// clock: now plus the time the server says is left. The server never sends an
// absolute timestamp, because comparing one to Date.now() breaks for any client
// whose clock is skewed from the server's (a fast clock shows the window as
// already over).
function ttLocalDeadline(payload) {
    return Date.now() + payload.remainingMs;
}

// handleStealJoin shows the join-window modal. The turn player cannot steal
// their own placement, so they see the countdown with no Steal button — "am
// I the turn player" is read off the already-rendered board (the same
// technique the turn timer uses), since this broadcast is identical for
// everyone and carries no per-viewer eligibility of its own. Whether the
// original placement was actually right or wrong is deliberately never
// revealed here — see steal.go's doc comment — so the heading must not
// assert either way.
function handleStealJoin(payload) {
    const amCurrentPlayer = !!document.querySelector("#tt-board .player-card.is-current.is-me");

    let hint = "";
    if (payload.hasLowerYear && payload.hasUpperYear) {
        hint = "They placed it between " + payload.lowerYear + " and " + payload.upperYear + ".";
    } else if (payload.hasUpperYear) {
        hint = "They placed it before " + payload.upperYear + ".";
    } else if (payload.hasLowerYear) {
        hint = "They placed it after " + payload.lowerYear + ".";
    }

    ttShowStealModal({
        heading: "Steal it?",
        hint: hint,
        deadlineMs: ttLocalDeadline(payload),
        showJoinButton: !amCurrentPlayer,
    });
}

// handleStealTurn shows the active-stealer's-turn modal. The active stealer
// places using the ordinary drop-zone buttons on their own timeline shelf
// (already wired to POST /attempt-steal for them by the board fragment,
// refreshed right after this), not from within this modal — this is a
// spectator countdown that also tells the active stealer their own turn has
// begun.
function handleStealTurn(payload) {
    const myRow = document.querySelector("#tt-board .player-card.is-me");
    const myPlayerId = myRow ? myRow.dataset.playerId : null;
    const amStealer = !!(myPlayerId && payload.stealerId === myPlayerId);

    ttShowStealModal({
        heading: amStealer ? "Your turn to steal!" : payload.stealerName + " is attempting to steal it",
        hint: amStealer ? "Place it on your own timeline before time runs out." : "",
        deadlineMs: ttLocalDeadline(payload),
        showJoinButton: false,
        blocking: !amStealer,
    });
}

// ---------------------------------------------------------------- challenge
//
// A challenge is a player saying the game wronged them and asking the table to
// vote on what they are owed (see api/tracktimeline/challenge.go). The form and
// the vote are both full-screen dialogs: while one is open nothing else in the
// game can happen, and the server enforces that too, so this is only the
// face of it. The vote countdown runs on the time-left the server sends, never
// an absolute timestamp, so a player's clock cannot break it.

let ttChallengeInterval = null;
let ttChallengeMyVote = { id: "", vote: "" };
let ttChallengeLoaded = false;

function ttChallengeEl(tag, className, text) {
    const el = document.createElement(tag);
    if (className) el.className = className;
    if (text !== undefined) el.textContent = text;
    return el;
}

function ttCloseChallengeModal() {
    if (ttChallengeInterval) {
        clearInterval(ttChallengeInterval);
        ttChallengeInterval = null;
    }
    const existing = document.getElementById("tt-challenge-modal");
    if (existing) existing.remove();
}

// ttEndChallengeWindow runs when the next song starts: the Challenge button goes
// and an unsent form is dropped (the server would refuse it now anyway).
function ttEndChallengeWindow() {
    const button = document.getElementById("tt-challenge-btn");
    if (button) {
        const row = button.closest(".challenge-row");
        if (row) row.remove();
    }
    const modal = document.getElementById("tt-challenge-modal");
    if (modal && modal.dataset.mode === "form") ttCloseChallengeModal();
}

function ttLoadChallengeOnce() {
    if (ttChallengeLoaded || !ttLobbyId) return;
    ttChallengeLoaded = true;
    fetch("/api/track-timeline/" + ttLobbyId + "/challenge", { cache: "no-store" })
        .then((r) => r.json())
        .then((info) => {
            if (!info.open || !info.challenge) return;
            if (info.myVote) ttChallengeMyVote = { id: info.challenge.id, vote: info.myVote };
            ttShowChallengeVote(info.challenge);
        })
        .catch((e) => console.error("[TrackTimeline] challenge load failed:", e));
}

function ttOpenChallengeForm() {
    const openButton = document.getElementById("tt-challenge-btn");
    const maxTokens = (openButton && parseInt(openButton.getAttribute("data-max-tokens"), 10)) || 10;

    ttCloseChallengeModal();

    const backdrop = ttChallengeEl("div", "tt-popup-backdrop tt-challenge-backdrop");
    backdrop.id = "tt-challenge-modal";
    backdrop.dataset.mode = "form";
    const popup = ttChallengeEl("div", "tt-popup tt-challenge-popup");

    popup.appendChild(ttChallengeEl("div", "tt-popup-artist", "Challenge"));
    popup.appendChild(ttChallengeEl("div", "tt-challenge-hint",
        "Think the game got something wrong? Say what, and what you're owed. If most of the others agree, you get it. " +
        "If they don't, you're out of challenges for this game."));

    const reason = ttChallengeEl("textarea", "tt-challenge-reason");
    reason.rows = 3;
    reason.maxLength = 300;
    reason.placeholder = "What went wrong? (e.g. the AI judge marked my right answer wrong)";
    popup.appendChild(reason);

    const kindRow = ttChallengeEl("div", "tt-challenge-kinds");
    const makeRadio = (value, label, checked) => {
        const wrap = ttChallengeEl("label", "tt-challenge-kind");
        const input = document.createElement("input");
        input.type = "radio";
        input.name = "tt-challenge-kind";
        input.value = value;
        input.checked = checked;
        wrap.appendChild(input);
        wrap.appendChild(document.createTextNode(" " + label));
        kindRow.appendChild(wrap);
        return input;
    };
    const cardRadio = makeRadio("card", "A free card", true);
    const tokensRadio = makeRadio("tokens", "Tokens", false);
    popup.appendChild(kindRow);

    const tokensRow = ttChallengeEl("div", "tt-challenge-tokens");
    tokensRow.style.display = "none";
    tokensRow.appendChild(document.createTextNode("How many? "));
    const tokens = document.createElement("input");
    tokens.type = "number";
    tokens.min = "1";
    tokens.max = String(maxTokens);
    tokens.value = "1";
    tokensRow.appendChild(tokens);
    tokensRow.appendChild(document.createTextNode(" (up to " + maxTokens + ")"));
    popup.appendChild(tokensRow);

    const sync = () => { tokensRow.style.display = tokensRadio.checked ? "" : "none"; };
    cardRadio.addEventListener("change", sync);
    tokensRadio.addEventListener("change", sync);

    const error = ttChallengeEl("div", "tt-challenge-error");
    popup.appendChild(error);

    const actions = ttChallengeEl("div", "tt-confirm-actions");
    const submit = ttChallengeEl("button", "btn-small", "Submit challenge");
    submit.type = "button";
    const cancel = ttChallengeEl("button", "btn-small btn-secondary", "Cancel");
    cancel.type = "button";
    cancel.addEventListener("click", ttCloseChallengeModal);
    submit.addEventListener("click", () => {
        error.textContent = "";
        submit.disabled = true;
        const body = new URLSearchParams({
            kind: tokensRadio.checked ? "tokens" : "card",
            tokens: tokens.value,
            reason: reason.value,
        });
        fetch("/api/track-timeline/" + ttLobbyId + "/challenge", {
            method: "POST",
            headers: { "Content-Type": "application/x-www-form-urlencoded" },
            body: body,
        })
            .then((r) => r.text().then((text) => ({ ok: r.ok, text: text })))
            .then((result) => {
                if (!result.ok) {
                    error.textContent = result.text;
                    submit.disabled = false;
                    return;
                }
                // The server's challenge: broadcast has usually already swapped
                // this form for the vote by now; only close the form itself.
                const modal = document.getElementById("tt-challenge-modal");
                if (modal && modal.dataset.mode === "form") ttCloseChallengeModal();
            })
            .catch((e) => {
                console.error("[TrackTimeline] challenge failed:", e);
                error.textContent = "Could not send the challenge.";
                submit.disabled = false;
            });
    });
    actions.appendChild(submit);
    actions.appendChild(cancel);
    popup.appendChild(actions);

    backdrop.appendChild(popup);
    document.body.appendChild(backdrop);
    reason.focus();
}

function ttPostChallengeAction(path, form) {
    return fetch("/api/track-timeline/" + ttLobbyId + "/challenge/" + path, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams(form || {}),
    }).then((r) => r.text().then((text) => ({ ok: r.ok, text: text })));
}

function ttShowChallengeVote(payload) {
    ttCloseChallengeModal();

    const myRow = document.querySelector("#tt-board .player-card.is-me");
    const myPlayerId = myRow ? myRow.dataset.playerId : null;
    const isChallenger = !!(myPlayerId && payload.challengerId === myPlayerId);
    const myVote = ttChallengeMyVote.id === payload.id ? ttChallengeMyVote.vote : "";

    const backdrop = ttChallengeEl("div", "tt-popup-backdrop tt-challenge-backdrop");
    backdrop.id = "tt-challenge-modal";
    backdrop.dataset.mode = "vote";
    const popup = ttChallengeEl("div", "tt-popup tt-challenge-popup");

    popup.appendChild(ttChallengeEl("div", "tt-popup-artist",
        isChallenger ? "Your challenge" : payload.challengerName + " is challenging"));
    popup.appendChild(ttChallengeEl("div", "tt-challenge-reason-text", "“" + payload.reason + "”"));
    popup.appendChild(ttChallengeEl("div", "tt-challenge-claim",
        "Wants: " + (payload.kind === "card"
            ? "a free card"
            : payload.tokens + (payload.tokens === 1 ? " token" : " tokens"))));

    const ring = ttCreateCountdownRing();
    popup.appendChild(ring.element);
    popup.appendChild(ttChallengeEl("div", "tt-challenge-status",
        payload.voted + " of " + payload.eligible + " voted"));

    const actions = ttChallengeEl("div", "tt-confirm-actions");
    const note = ttChallengeEl("div", "tt-challenge-note");
    if (isChallenger) {
        note.textContent = "Waiting for the table to vote…";
        const withdraw = ttChallengeEl("button", "btn-small btn-secondary", "Withdraw");
        withdraw.type = "button";
        withdraw.addEventListener("click", () => {
            withdraw.disabled = true;
            ttPostChallengeAction("withdraw").then((r) => { if (!r.ok) note.textContent = r.text; });
        });
        actions.appendChild(withdraw);
    } else if (myVote) {
        note.textContent = "You voted " + myVote + ". Waiting for the others…";
    } else {
        const agreeButton = ttChallengeEl("button", "btn-small", "Agree");
        agreeButton.type = "button";
        const disagreeButton = ttChallengeEl("button", "btn-small btn-secondary", "Disagree");
        disagreeButton.type = "button";
        const cast = (agree) => {
            agreeButton.disabled = true;
            disagreeButton.disabled = true;
            ttPostChallengeAction("vote", { agree: agree ? "1" : "0" }).then((r) => {
                if (r.ok) {
                    ttChallengeMyVote = { id: payload.id, vote: agree ? "agree" : "disagree" };
                    note.textContent = "Vote recorded. Waiting for the others…";
                } else {
                    note.textContent = r.text;
                }
            });
        };
        agreeButton.addEventListener("click", () => cast(true));
        disagreeButton.addEventListener("click", () => cast(false));
        actions.appendChild(agreeButton);
        actions.appendChild(disagreeButton);
    }
    popup.appendChild(actions);
    popup.appendChild(note);

    backdrop.appendChild(popup);
    document.body.appendChild(backdrop);

    const deadline = Date.now() + payload.remainingMs;
    const total = Math.max(payload.windowMs || 0, payload.remainingMs, 1000);
    const tick = () => {
        const remaining = deadline - Date.now();
        ring.update(Math.max(0, remaining), total);
        if (remaining <= 0 && ttChallengeInterval) {
            clearInterval(ttChallengeInterval);
            ttChallengeInterval = null;
        }
    };
    tick();
    ttChallengeInterval = setInterval(tick, 100);
}

// ttToggleExactYear swaps the timeline's normal +-slot placement buttons for
// a single year input + lock-in button, and back. The buttons live in the
// board fragment (#tt-board), a separate htmx fragment from this checkbox
// (#tt-current-card), so this reaches across via plain DOM queries rather
// than a server round-trip -- purely a pre-submission UI toggle, nothing to
// validate server-side until the player actually submits.
function ttToggleExactYear() {
    const useExactYear = document.getElementById("tt-use-exact-year");
    const on = !!(useExactYear && useExactYear.checked);

    const yearRow = document.getElementById("tt-exact-year-row");
    if (yearRow) yearRow.style.display = on ? "" : "none";

    syncPlacementButtons();
    if (on) ttValidateExactYear();
}

// Lock In Year stays off until Play has been clicked, the wager is an integer
// from 1 through the player's current tokens, and the year is four digits.
// Overshooting the token max shows an inline "not enough tokens" hint.
function ttValidateExactYear() {
    const year = document.getElementById("tt-exact-year");
    const wager = document.getElementById("tt-year-wager");
    const lock = document.getElementById("tt-lock-year");
    const wagerError = document.getElementById("tt-year-wager-error");
    if (!wager || !lock) return;

    const max = parseInt(wager.getAttribute("max"), 10);
    const raw = String(wager.value).trim();
    const n = parseInt(raw, 10);
    const hasNumber = raw !== "" && Number.isInteger(n);
    const notEnough = hasNumber && Number.isInteger(max) && n > max;
    if (wagerError) {
        wagerError.hidden = !notEnough;
    }
    const wagerOk = hasNumber && n >= 1 && Number.isInteger(max) && n <= max;
    const yearOk = !!(year && /^\d{4}$/.test(String(year.value).trim()));
    const played = ttPlaybackStartedThisRound;
    const listenGateOpen = ttListenGateSatisfied();
    lock.disabled = !(played && listenGateOpen && wagerOk && yearOk);
    if (!played) {
        lock.title = "Play the song first";
    } else if (!listenGateOpen) {
        lock.title = "Give everyone a chance to guess — " + ttListenGateRemainingSeconds() + "s left before you can lock in";
    } else if (notEnough) {
        lock.title = "Not enough tokens for that wager.";
    } else if (!wagerOk) {
        lock.title = "Wager must be between 1 and your tokens.";
    } else if (!yearOk) {
        lock.title = "Enter a 4-digit year.";
    } else {
        lock.title = "Lock in this year and spend the wager.";
    }
}

// -------------------------------------------------- in-game volume & audio progress

function ttInitVolume() {
    try {
        const savedVol = localStorage.getItem("tt_volume");
        if (savedVol !== null) {
            const parsed = parseInt(savedVol, 10);
            if (!isNaN(parsed) && parsed >= 0 && parsed <= 100) {
                ttVolume = parsed;
            }
        }
        const savedMute = localStorage.getItem("tt_muted");
        if (savedMute === "true") {
            ttMuted = true;
        }
    } catch (e) {
        // localStorage not available or blocked
    }
    ttApplyVolumeUI();
}

function ttSetVolume(val) {
    const vol = parseInt(val, 10);
    if (isNaN(vol) || vol < 0 || vol > 100) return;
    ttVolume = vol;
    try {
        localStorage.setItem("tt_volume", vol.toString());
    } catch (e) {}

    if (vol > 0 && ttMuted) {
        ttMuted = false;
        try {
            localStorage.setItem("tt_muted", "false");
        } catch (e) {}
    }
    if (vol === 0) {
        ttMuted = true;
        try {
            localStorage.setItem("tt_muted", "true");
        } catch (e) {}
    }
    ttApplyVolumeToPlayer();
    ttApplyVolumeUI();
}

function ttToggleMute() {
    ttMuted = !ttMuted;
    try {
        localStorage.setItem("tt_muted", ttMuted ? "true" : "false");
    } catch (e) {}
    if (!ttMuted && ttVolume === 0) {
        ttVolume = 50;
        try {
            localStorage.setItem("tt_volume", "50");
        } catch (e) {}
    }
    ttApplyVolumeToPlayer();
    ttApplyVolumeUI();
}

function ttApplyVolumeToPlayer() {
    if (!ttPlayer || !ttPlayerReady) return;
    try {
        if (ttMuted) {
            ttPlayer.mute();
        } else {
            ttPlayer.unMute();
            ttPlayer.setVolume(ttVolume);
        }
    } catch (e) {}
}

function ttApplyVolumeUI() {
    const displayVol = ttMuted ? 0 : ttVolume;
    const iconClass = ttMuted || displayVol === 0
        ? "bi bi-volume-mute-fill"
        : displayVol < 50
            ? "bi bi-volume-down-fill"
            : "bi bi-volume-up-fill";

    document.querySelectorAll(".tt-volume-slider").forEach((el) => {
        el.value = displayVol;
    });
    document.querySelectorAll(".tt-volume-val").forEach((el) => {
        el.textContent = displayVol + "%";
    });
    document.querySelectorAll(".tt-volume-icon").forEach((el) => {
        el.className = "tt-volume-icon " + iconClass;
    });
    document.querySelectorAll(".tt-volume-btn").forEach((el) => {
        el.title = ttMuted ? "Unmute" : "Mute";
        el.classList.toggle("is-muted", ttMuted);
    });
}

function ttFormatTime(sec) {
    const s = Math.floor(sec || 0);
    const m = Math.floor(s / 60);
    const rem = s % 60;
    return m + ":" + (rem < 10 ? "0" : "") + rem;
}

function ttStartClipProgressTimer() {
    ttStopClipProgressTimer();
    ttUpdateClipProgress();
    ttClipProgressInterval = setInterval(ttUpdateClipProgress, 100);
}

function ttStopClipProgressTimer() {
    if (ttClipProgressInterval) {
        clearInterval(ttClipProgressInterval);
        ttClipProgressInterval = null;
    }
}

function ttResetClipProgress() {
    const dur = ttClipDurationSeconds > 0 ? ttClipDurationSeconds : 20;
    document.querySelectorAll(".tt-clip-progress-fill").forEach((el) => {
        el.style.width = "0%";
    });
    document.querySelectorAll(".tt-clip-elapsed-text").forEach((el) => {
        el.textContent = "0:00";
    });
    document.querySelectorAll(".tt-clip-duration-text").forEach((el) => {
        el.textContent = ttFormatTime(dur);
    });
    document.querySelectorAll(".tt-clip-countdown-text").forEach((el) => {
        el.textContent = Math.ceil(dur) + "s left";
    });
    document.querySelectorAll(".tt-clip-pulse-dot").forEach((el) => {
        el.classList.remove("is-active");
    });
    document.querySelectorAll(".tt-platter-ring-fill").forEach((el) => {
        el.style.strokeDashoffset = "0";
    });
    document.querySelectorAll(".tt-platter-countdown-val").forEach((el) => {
        el.textContent = Math.ceil(dur) + "s";
    });
}

function ttFinishClipProgress() {
    const dur = ttClipDurationSeconds > 0 ? ttClipDurationSeconds : 20;
    document.querySelectorAll(".tt-clip-progress-fill").forEach((el) => {
        el.style.width = "100%";
    });
    document.querySelectorAll(".tt-clip-elapsed-text").forEach((el) => {
        el.textContent = ttFormatTime(dur);
    });
    document.querySelectorAll(".tt-clip-countdown-text").forEach((el) => {
        el.textContent = "Ended";
    });
    document.querySelectorAll(".tt-clip-pulse-dot").forEach((el) => {
        el.classList.remove("is-active");
    });
    document.querySelectorAll(".tt-platter-ring-fill").forEach((el) => {
        el.style.strokeDashoffset = "295.31";
    });
    document.querySelectorAll(".tt-platter-countdown-val").forEach((el) => {
        el.textContent = "0s";
    });
}

function ttUpdateClipProgress() {
    ttApplySpin();
    let isPlaying = false;
    let currentTime = 0;
    if (ttPlayer && ttPlayerReady) {
        try {
            isPlaying = ttPlayer.getPlayerState() === YT.PlayerState.PLAYING;
            currentTime = ttPlayer.getCurrentTime();
        } catch (e) {}
    }

    document.querySelectorAll(".tt-clip-pulse-dot").forEach((el) => {
        el.classList.toggle("is-active", isPlaying);
    });

    let duration = ttClipDurationSeconds;
    if (duration <= 0 && ttPlayer && ttPlayerReady) {
        try {
            duration = (ttPlayer.getDuration() || 0) - ttClipStartSeconds;
        } catch (e) {}
    }
    if (duration <= 0) duration = 20;

    let elapsed = Math.max(0, currentTime - ttClipStartSeconds);
    if (ttClipFinished) elapsed = duration;
    elapsed = Math.min(elapsed, duration);

    const remaining = Math.max(0, duration - elapsed);
    const fraction = duration > 0 ? (elapsed / duration) : 0;
    const percent = Math.min(100, Math.max(0, fraction * 100));

    document.querySelectorAll(".tt-clip-progress-fill").forEach((el) => {
        el.style.width = percent + "%";
    });
    document.querySelectorAll(".tt-clip-elapsed-text").forEach((el) => {
        el.textContent = ttFormatTime(elapsed);
    });
    document.querySelectorAll(".tt-clip-duration-text").forEach((el) => {
        el.textContent = ttFormatTime(duration);
    });
    document.querySelectorAll(".tt-clip-countdown-text").forEach((el) => {
        if (ttClipFinished) {
            el.textContent = "Ended";
        } else {
            el.textContent = Math.ceil(remaining) + "s left";
        }
    });

    // Circular Platter Countdown Ring (radius 47, circumference 295.31)
    const ringPerimeter = 295.31;
    const offset = ringPerimeter * fraction;
    document.querySelectorAll(".tt-platter-ring-fill").forEach((el) => {
        el.style.strokeDashoffset = offset.toFixed(2);
    });
    document.querySelectorAll(".tt-platter-countdown-val").forEach((el) => {
        el.textContent = (ttClipFinished ? 0 : Math.ceil(remaining)) + "s";
    });
}

function ttCreateCountdownRing() {
    const wrap = document.createElement("div");
    wrap.className = "tt-countdown-ring-wrap";

    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "tt-modal-countdown-svg");
    svg.setAttribute("viewBox", "0 0 100 100");

    const track = document.createElementNS("http://www.w3.org/2000/svg", "circle");
    track.setAttribute("class", "tt-modal-ring-track");
    track.setAttribute("cx", "50");
    track.setAttribute("cy", "50");
    track.setAttribute("r", "44");
    svg.appendChild(track);

    const ring = document.createElementNS("http://www.w3.org/2000/svg", "circle");
    ring.setAttribute("class", "tt-modal-ring-fill");
    ring.setAttribute("cx", "50");
    ring.setAttribute("cy", "50");
    ring.setAttribute("r", "44");
    svg.appendChild(ring);

    wrap.appendChild(svg);

    const countdown = document.createElement("div");
    countdown.className = "tt-popup-year tt-steal-countdown";
    wrap.appendChild(countdown);

    return {
        element: wrap,
        textEl: countdown,
        update: (remainingMs, totalMs) => {
            const seconds = Math.max(0, Math.ceil(remainingMs / 1000));
            countdown.textContent = seconds.toString();
            const fraction = totalMs > 0 ? Math.max(0, Math.min(1, remainingMs / totalMs)) : 0;
            const perimeter = 276.46; // 2 * PI * 44
            const offset = perimeter * (1 - fraction);
            ring.style.strokeDashoffset = offset.toFixed(2);
            ring.classList.toggle("is-warning", seconds <= 5);
        },
    };
}

if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", ttInitVolume);
} else {
    ttInitVolume();
}

