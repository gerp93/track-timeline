# Claude sandbox tooling

Not part of the shipped app. This directory exists so a future Claude Code
session working in a fresh, ephemeral cloud sandbox (no MariaDB, no Docker
registry access, no network to youtube.com/cdn.jsdelivr.net) doesn't have to
re-derive the same setup from scratch every time. It's for Claude's own
efficiency, not a supported local-dev path for human contributors -- the
real project docs are the root `README.md` and `CLAUDE.md`.

Everything here was built and verified against the actual sandbox this was
written in; if the environment changes (a different base image, different
network policy), some of it may need adjusting.

## 1. Stand up MariaDB

```
bash dev/claude-sandbox/setup-mariadb.sh
```

Installs `mariadb-server` via `apt-get` (the Docker Hub registry is blocked
by the sandbox's egress policy, but `archive.ubuntu.com` is reachable, so
`docker run mariadb:...` is a dead end here -- don't bother retrying it),
starts `mariadbd-safe` manually (no init system in the container), and
creates a `tt_app` TCP user (`mysql_native_password`, since the app's DB
layer always connects over TCP and `root`@`localhost`'s default
`unix_socket` auth plugin doesn't accept a password over TCP) plus both
databases the rest of this tooling expects: `track_timeline_dev` (manual
play / Playwright) and `tt_e2e` (the Go e2e suite). Idempotent -- re-run
freely in the same container.

## 2. Build and run the real server

```
cd src
export TRACK_TIMELINE_SQL_HOST=127.0.0.1
export TRACK_TIMELINE_SQL_USER=tt_app
export TRACK_TIMELINE_SQL_PASSWORD='PlaytestDbPass123!'
export TRACK_TIMELINE_SQL_DATABASE=track_timeline_dev
export TRACK_TIMELINE_ADMIN_PASSWORD='AdminPass123!'   # only used the very first time (empty USER table)
go build -o /tmp/track-timeline-server .
/tmp/track-timeline-server
```

Serves on `:2016` by default (`TRACK_TIMELINE_PORT` to change it). Schema is
applied automatically on boot (framework schema, then this game's), same as
production.

**Run it with the Bash tool's own `run_in_background: true`, not a shell
`&`/`nohup`.** A plain `&` gets killed the moment the tool call that spawned
it returns -- the process looks like it started (no error), but the very
next `curl` to `:2016` fails to connect, and the server's own log shows
nothing past the earlier failed attempt. Backgrounding it through the tool
itself is what actually keeps it alive across tool calls.

## 3. Seed test data

```
cd src
export TRACK_TIMELINE_SQL_HOST=127.0.0.1 TRACK_TIMELINE_SQL_USER=tt_app \
       TRACK_TIMELINE_SQL_PASSWORD='PlaytestDbPass123!' TRACK_TIMELINE_SQL_DATABASE=track_timeline_dev
go run -tags devsandboxseed ./devsandboxseed.go
```

Creates three players (Alice/Bob/Carol, password `PlaytestPass123!`), a
6-card deck (including a `Hunter` / `Björk` card -- guess it by hand in the
game to re-exercise the byte-vs-rune mojibake fix, see
`api/tracktimeline/round.go`'s `truncateRunes`), a lobby with everyone
already joined, **and its game + draw pile** (`database.CreateGame` +
`database.InitializeDrawPile` -- a lobby created any other way has no game,
and the lobby page redirects straight back to `/track-timeline/lobbies` the
instant it notices, see `api/pages/pages.go`'s `TrackTimelineLobby`). Prints
the lobby URL. Safe to re-run -- it logs and continues past a duplicate
user, and always creates a fresh deck/lobby/game.

Each run's lobby only lives as long as some browser stays connected to it:
closing every context/tab that joined it (the framework's usual
last-client-disconnect rule, see step 4 below) deletes the lobby, its game,
and its draw pile right along with it -- a lobby URL from a previous run is
not reusable once nothing is still connected to it. Re-run this script for a
fresh one rather than reusing an old printed URL.

`devsandboxseed.go` lives in `src/`, gated behind the `devsandboxseed` build
tag, **not** as its own module under this directory. A standalone module
here (with a `replace` pointing at `../../src`) was the first thing tried --
`go mod tidy` failed because this sandbox has no route to the Go module
proxy, and the local module cache only holds the exact versions `src/`'s own
`go.sum` already resolved, not the full transitive closure a fresh `tidy`
needs to re-derive (it got stuck wanting `tidwall/pretty@v1.2.0`'s go.mod,
present in `src/go.sum` by hash but never separately fetched). Building
inside the same module sidesteps that entirely by reusing versions already
resolved for the real server. The build tag keeps `go build ./...` / `go vet
./...` / `go test ./...` from ever seeing the file, and running it as a
single named file (`go run -tags devsandboxseed ./devsandboxseed.go`, not
`go run .`) keeps its `func main` from colliding with `main.go`'s.

## 4. Manual / Playwright verification

Chromium is at `/opt/pw-browsers/chromium-1194/chrome-linux/chrome`
(Playwright itself: `/opt/node22/lib/node_modules/playwright`). Two things
the real pages load that this sandbox can't reach directly:

- `youtube.com`'s IFrame Player API -- stubbed by `playwright/yt-stub.js`.
- `cdn.jsdelivr.net` (htmx, bootstrap-icons) -- vendor local copies once per
  container:

  ```
  cd /tmp && npm pack htmx.org@2.0.7 bootstrap-icons@1.11.3
  mkdir -p /tmp/htmx-pkg /tmp/bi-pkg
  tar -xzf htmx.org-2.0.7.tgz -C /tmp/htmx-pkg
  tar -xzf bootstrap-icons-1.11.3.tgz -C /tmp/bi-pkg
  ```

  (Versions match what the framework's `base.html` currently pins -- if
  pages stop rendering, check there for a bump before assuming this is
  stale.)

`playwright/sandbox-helpers.js` wraps the routing boilerplate plus two
sandbox-specific quirks worth knowing about up front:

- **This app's confirm dialogs are not real `window.confirm()`.** `hx-confirm`
  is intercepted by `global.js` and rendered as an in-page
  `<dialog id="confirmation-dialog">`; Playwright's
  `page.on("dialog", ...)` never fires for it. Use the exported
  `confirmYes(page)` instead.
- **Closing the last browser context deletes the lobby.** The framework
  drops a lobby when its last websocket client disconnects, cascading away
  everything hanging off it (including `TRACK_TIMELINE_LOG_*`-adjacent rows
  that aren't already FK-free). Run any DB assertions *before* calling
  `browser.close()` (or keep a second context open throughout), not after.

Minimal example:

```js
const fs = require("fs");
const pw = require("/opt/node22/lib/node_modules/playwright");
const { newPlayerContext, login, confirmYes } = require("./playwright/sandbox-helpers");

const assets = {
  htmxJs: fs.readFileSync("/tmp/htmx-pkg/package/dist/htmx.js", "utf8"),
  biCss: fs.readFileSync("/tmp/bi-pkg/package/font/bootstrap-icons.min.css", "utf8"),
  biWoff2: fs.readFileSync("/tmp/bi-pkg/package/font/fonts/bootstrap-icons.woff2"),
  biWoff: fs.readFileSync("/tmp/bi-pkg/package/font/fonts/bootstrap-icons.woff"),
};

(async () => {
  const browser = await pw.chromium.launch({
    executablePath: "/opt/pw-browsers/chromium-1194/chrome-linux/chrome",
  });
  const ctx = await newPlayerContext(browser, assets);
  await login(ctx, { name: "Alice", password: "PlaytestPass123!" });
  const page = await ctx.newPage();
  await page.goto("http://127.0.0.1:2016/track-timeline/<lobby-id-from-step-3>", {
    waitUntil: "networkidle",
  });
  await page.click('button:has-text("Start Game")');
  // ... drive the game, screenshot, assert ...
  await browser.close();
})();
```

## 5. Run the Go test suite

```
bash dev/claude-sandbox/reset-e2e-db.sh   # every time -- the suite leaves fixed-name rows behind
cd src
TRACK_TIMELINE_SQL_HOST=127.0.0.1 TRACK_TIMELINE_SQL_USER=tt_app \
TRACK_TIMELINE_SQL_PASSWORD='PlaytestDbPass123!' TRACK_TIMELINE_SQL_DATABASE=tt_e2e \
go test ./...
```

The e2e/page-render tests refuse to run unless `TRACK_TIMELINE_SQL_DATABASE`
starts with `tt_e2e` (their own safety guard, not this tooling's) and will
otherwise just skip cleanly -- so it's always safe to run `go test ./...`
without a database at all; you only need the steps above when you actually
want those tests to execute.
