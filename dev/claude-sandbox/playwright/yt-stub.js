// Stand-in for the real YouTube IFrame Player API (no network access to
// youtube.com in this sandbox). Mimics just enough of the real surface for
// track-timeline.js's playSong/pauseSong/stopSong/onStateChange logic to
// run exactly as it would against real playback -- state numbers match the
// real API (UNSTARTED=-1, ENDED=0, PLAYING=1, PAUSED=2, BUFFERING=3, CUED=5).
(function () {
  function StubPlayer(elementId, config) {
    this._config = config || {};
    this._state = -1;
    var self = this;
    window.__ytStubPlayers = window.__ytStubPlayers || [];
    window.__ytStubPlayers.push(this);
    setTimeout(function () {
      if (self._config.events && self._config.events.onReady) {
        self._config.events.onReady({ target: self });
      }
    }, 30);
  }
  StubPlayer.prototype._setState = function (state) {
    this._state = state;
    if (this._config.events && this._config.events.onStateChange) {
      this._config.events.onStateChange({ data: state, target: this });
    }
  };
  StubPlayer.prototype.loadVideoById = function (opts) {
    if (typeof opts === "string") opts = { videoId: opts };
    this._videoId = opts.videoId;
    this._startSeconds = opts.startSeconds || 0;
    this._endSeconds = opts.endSeconds || 0;
    this._setState(1);
  };
  StubPlayer.prototype.playVideo = function () {
    // Simulate real playback position so track-timeline.js's clip-progress
    // polling (getCurrentTime/getDuration) has something real to show,
    // rather than always reading 0. Tracks wall-clock elapsed since the
    // last play, offset by _elapsedAtPause so pause/resume accumulates
    // correctly instead of resetting.
    this._playStartedAtMs = Date.now();
    this._setState(1);
  };
  StubPlayer.prototype.pauseVideo = function () {
    this._elapsedAtPause = this._currentElapsed();
    this._playStartedAtMs = null;
    this._setState(2);
  };
  StubPlayer.prototype.stopVideo = function () {
    this._playStartedAtMs = null;
    this._elapsedAtPause = 0;
    this._setState(-1);
  };
  StubPlayer.prototype._currentElapsed = function () {
    var base = this._elapsedAtPause || 0;
    if (this._playStartedAtMs) {
      base += (Date.now() - this._playStartedAtMs) / 1000;
    }
    return base;
  };
  StubPlayer.prototype.getPlayerState = function () {
    return this._state;
  };
  StubPlayer.prototype.getCurrentTime = function () {
    return (this._startSeconds || 0) + this._currentElapsed();
  };
  StubPlayer.prototype.getDuration = function () {
    // Arbitrary but plausible full-video length, distinct from any single
    // clip's startSeconds/endSeconds window, so duration-minus-start clip
    // length fallbacks have a realistic value to compute from.
    return 200;
  };
  StubPlayer.prototype.seekTo = function () {};
  StubPlayer.prototype.destroy = function () {};

  window.YT = {
    Player: StubPlayer,
    PlayerState: { UNSTARTED: -1, ENDED: 0, PLAYING: 1, PAUSED: 2, BUFFERING: 3, CUED: 5 },
  };

  if (typeof window.onYouTubeIframeAPIReady === "function") {
    window.onYouTubeIframeAPIReady();
  }
})();
