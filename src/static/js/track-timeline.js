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

const TT_STATUS_MESSAGE_MS = 8000;

// ---------------------------------------------------------------- websocket

function initTrackTimeline(lobbyId, turnTimerSeconds) {
    ttLobbyId = lobbyId;
    setTurnTimerSeconds(turnTimerSeconds || 0);

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
            ttSyncSelfStatus();
            syncPlaybackUI();
            restartTurnTimer();
        }
        if (event.detail.target && event.detail.target.id === "tt-current-card") {
            syncPlaybackUI();
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
                restartTurnTimer();
            })
            .catch((e) => console.error("[TrackTimeline] board refresh failed:", e));
    }

    const pile = document.getElementById("tt-draw-pile-count");
    if (pile) {
        fetch(base + "/draw-pile-count", { cache: "no-store" })
            .then((r) => r.text())
            .then((count) => { pile.textContent = count; })
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
                }
                if (event.data === YT.PlayerState.ENDED) {
                    // The clip ran its full length. Play must not silently
                    // restart it from the top after this — hearing it again
                    // is what the paid restart is for.
                    ttClipFinished = true;
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
    document.querySelectorAll(".tt-record").forEach((el) => {
        el.classList.toggle("is-spinning", playing);
    });
    document.querySelectorAll(".tt-visualizer").forEach((el) => {
        el.classList.toggle("is-active", playing);
    });
    document.querySelectorAll(".tt-tonearm").forEach((el) => {
        el.classList.toggle("is-active", playing);
    });

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
            ? "The clip has finished — restart it for a token to hear it again"
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
            replayBtn.title = "You need a token to restart.";
        } else if (!ttClipListenedThisRound) {
            replayBtn.title = "Hear the clip through (or pause it) before restarting.";
        } else {
            replayBtn.title = "Restart this clip from the beginning, once, for a token";
        }
    }

    const skipBtn = document.getElementById("tt-skip-btn");
    if (skipBtn) {
        const noTokens = skipBtn.getAttribute("data-no-tokens") === "1";
        skipBtn.disabled = noTokens || !ttPlaybackStartedThisRound;
        if (noTokens) {
            skipBtn.title = "You need a token to skip.";
        } else if (!ttPlaybackStartedThisRound) {
            skipBtn.title = "Play the song first";
        } else {
            skipBtn.title = "Skip this song for a token and draw another";
        }
    }

    syncPlacementButtons();
}

// Place (+ drop zones and exact-year lock-in) stays off until Play has been
// clicked this round — same gate as Skip. Steal-turn drop zones are exempt:
// the song already played on the original turn, and stopSong clears the flag.
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
        const blocked = !ttPlaybackStartedThisRound || exactYearOn;
        btn.disabled = blocked;
        btn.classList.toggle("is-disabled", blocked);
        if (!ttPlaybackStartedThisRound) {
            btn.title = "Play the song first";
        } else if (exactYearOn) {
            btn.title = "Using exact-year wager — lock in below";
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
    ttTimerHeldForPlayback = false;
    ttClipReachedPlaying = false;

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

    const countdown = document.createElement("div");
    countdown.className = "tt-popup-year tt-steal-countdown";
    popup.appendChild(countdown);

    wrap.appendChild(popup);
    document.body.appendChild(wrap);

    const badgeEl = document.getElementById("tt-turn-timer");
    let expired = false;

    const tick = () => {
        const remainingMs = ttTurnTimerDeadlineMs - Date.now();
        const seconds = Math.max(0, Math.ceil(remainingMs / 1000));
        countdown.textContent = seconds.toString();
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

    if (payload.guessTokenWinnerName) {
        const guessLine = document.createElement("div");
        guessLine.className = "tt-popup-guess";
        var quoted = payload.guessTokenGuessText
            ? " — “" + payload.guessTokenGuessText + "”"
            : "";
        guessLine.textContent = payload.guessTokenWinnerName + " named it" + quoted + " — won 1 token";
        popup.appendChild(guessLine);
    }

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
// countdown on receipt), the steal countdown is server-authoritative: the
// deadline in the payload is a fixed instant computed by the server, and
// every client renders the same remaining time by recomputing
// deadlineMs - Date.now() each tick, rather than each starting its own timer
// from a duration. The server enforces the deadline itself (a scheduled
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

    const countdown = document.createElement("div");
    countdown.className = "tt-popup-year tt-steal-countdown";
    popup.appendChild(countdown);

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

    const tick = () => {
        const remainingMs = opts.deadlineMs - Date.now();
        if (remainingMs <= 0) {
            countdown.textContent = "0";
            clearInterval(ttStealInterval);
            ttStealInterval = null;
            return;
        }
        countdown.textContent = Math.ceil(remainingMs / 1000).toString();
    };
    tick();
    ttStealInterval = setInterval(tick, 200);
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
        deadlineMs: payload.deadlineMs,
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
        deadlineMs: payload.deadlineMs,
        showJoinButton: false,
        blocking: !amStealer,
    });
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
    lock.disabled = !(played && wagerOk && yearOk);
    if (!played) {
        lock.title = "Play the song first";
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
