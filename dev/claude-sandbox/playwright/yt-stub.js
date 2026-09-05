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
    this._setState(1);
  };
  StubPlayer.prototype.pauseVideo = function () {
    this._setState(2);
  };
  StubPlayer.prototype.stopVideo = function () {
    this._setState(-1);
  };
  StubPlayer.prototype.getPlayerState = function () {
    return this._state;
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
