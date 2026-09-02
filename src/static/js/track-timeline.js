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
    ttTurnTimerSeconds = turnTimerSeconds || 0;

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
            restartTurnTimer();
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
        playSong(payload.videoId, payload.startSeconds || 0);
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
        stopSong();
        showStatus(payload.bottomMessage);

        // Freeze the clock straight away: it belongs to a turn that has ended
        // and must not keep ticking behind the reveal.
        ttDeferTimerStart = true;
        if (typeof gsTimer !== "undefined") {
            gsTimer.stop();
        }

        showResultPopup(payload, () => {
            ttDeferTimerStart = false;
            refreshGame();
            if (payload.gameOver) {
                refreshControls();
            } else {
                restartTurnTimer();
            }
        });
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
        ttTurnTimerSeconds = parseInt(message.substring("turnTimer:".length)) || 0;
        showStatus(ttTurnTimerSeconds > 0
            ? "Turn timer set to " + ttTurnTimerSeconds + "s"
            : "Turn timer turned off");
        restartTurnTimer();
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

function refreshGame() {
    if (!ttLobbyId) return;
    const base = "/api/track-timeline/" + ttLobbyId;

    htmx.ajax("GET", base + "/current-card", { target: "#tt-current-card", swap: "innerHTML" });
    htmx.ajax("GET", base + "/players", { target: "#tt-players", swap: "innerHTML" });
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
                    playSong(pending.videoId, pending.startSeconds);
                }
            },
            onError: () => {
                showStatus("That video would not play. The player on turn can skip it.");
            },
        },
    });
};

function playSong(videoId, startSeconds) {
    if (!videoId) return;

    // The API may not have finished loading when the first song arrives; hold
    // it and play once the player reports ready.
    if (!ttPlayerReady || !ttPlayer) {
        ttPendingSong = { videoId: videoId, startSeconds: startSeconds };
        return;
    }

    try {
        ttPlayer.loadVideoById({ videoId: videoId, startSeconds: startSeconds || 0 });
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
    if (ttPlayer && ttPlayerReady) {
        try {
            ttPlayer.stopVideo();
        } catch (e) {
            // Nothing useful to do if the player is already gone.
        }
    }
    ttPendingSong = null;
    hideAudioUnlock();
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

function restartTurnTimer() {
    if (ttDeferTimerStart) return;
    doRestartTurnTimer();
}

function doRestartTurnTimer() {
    if (typeof gsTimer === "undefined") return;

    const timerEl = document.getElementById("tt-turn-timer");
    if (!timerEl) return;

    if (ttTurnTimerSeconds <= 0) {
        gsTimer.stop();
        return;
    }

    // Whose turn it is comes from the DOM rather than from JS state, so a
    // refreshed board is always the source of truth.
    const mine = document.querySelector(".player-row.is-current.is-me");
    const anyTurn = document.querySelector(".player-row.is-current");
    if (!anyTurn) {
        gsTimer.stop();
        return;
    }

    gsTimer.start(timerEl, ttTurnTimerSeconds, () => {
        // Only the player whose turn it is reports the timeout; the server
        // re-checks anyway, so a stale call cannot end somebody else's turn.
        if (mine) {
            fetch("/api/track-timeline/" + ttLobbyId + "/timeout", { method: "POST" })
                .catch(() => {});
        }
    });
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
    title.textContent = payload.title;
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

    if (payload.gameOver && payload.celebration) {
        const celebration = document.createElement("div");
        celebration.className = "tt-popup-celebration";
        celebration.textContent = payload.celebration;
        popup.appendChild(celebration);
    }

    if (payload.gameOver && payload.hasGif && payload.userId) {
        const gif = document.createElement("img");
        gif.className = "tt-popup-gif";
        gif.src = "/api/user/" + payload.userId + "/win-gif";
        gif.alt = "";
        popup.appendChild(gif);
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
    setTimeout(finish, payload.gameOver ? 6000 : 4000);
}
