# CLAUDE.md — track-timeline

Guidance for working in this repository. This file is a **style guide first,
an architecture map second**. It documents the conventions already in use so
that changes match the existing codebase. Match the surrounding code; do not
introduce new styles, formatters, or abstractions.

This repo shares its platform and style with
[card-judge](https://github.com/gerp93/card-judge) and
[timeline-trivia](https://github.com/gerp93/timeline-trivia) — all three
consume [`gameshell-framework`](https://github.com/gerp93/gameshell-framework)
and read as one author's codebase. When in doubt, check how timeline-trivia
does the same thing first — its rules (chronology, cards, decks, year
ranges) are the closest ancestor of this game's.

## What this is

**Track Timeline**: a Hitster-style music chronology party game. Players hear
a song (played from YouTube) and place it into their own timeline by release
year. A correct placement grows the timeline; getting it wrong opens a
**challenge window** where other players may spend a token to place the same
song in their own timeline and steal it if they get it right. Correctly
naming the song's artist and/or title earns tokens, which is what fuels the
challenge economy. First player to reach the configured number of cards wins.

Stack: **Go (stdlib `net/http`) + HTMX + `gorilla/websocket` + MariaDB.** No
web framework, no ORM, no build step for the front end. Audio is a hidden
YouTube IFrame player synchronized over the websocket — never a DOM-based
security boundary, just a courtesy (see "Real-time" below).

## Layout

The repo root is a thin wrapper. **All application code lives under `src/`,
which is the Go module root** (`module github.com/gerp93/track-timeline`, Go
1.22.5). The reusable platform lives in the separate
**`github.com/gerp93/gameshell-framework`** module (auth, page middleware,
user/lobby-shell/player-base data layer, **deck management**, shared chat
rendering, websocket hub, framework schema) — this repo holds only the game.

```
src/
  main.go                entry point: registers the Game impl + framework
                         params, DB connect, framework schema then game
                         schema, ALL route wiring, server
  go.mod                 module + framework dependency (pinned version tag)
  game/                  hooks.go — TrackTimeline implements gameshell.Game
  guess/                 the artist/title Judge interface (see below)
  api/                   game HTTP handlers, grouped by domain
    pages/                full-page renderers (package apiPages)
    access/               deck/lobby password-gate handlers (package apiAccess)
    card/                 card CRUD (package apiCard)
    category/             genre CRUD (package apiCategory)
    tracktimeline/         gameplay handlers (package apiTrackTimeline):
                           track-timeline.go (lobby setup, start/reset, play
                           song), round.go (placement, challenge, guess,
                           skip, timeout), fragments.go (HTMX GET fragments)
  database/               game data-access: one file per domain
    card.go, card-import.go  card CRUD + JSON bulk import
    category.go              genre CRUD
    lobby.go                 thin GetLobby reader (writes are framework-owned)
    track-timeline.go        game/draw-pile/round-phase/turn-order logic
    round.go                 placement, tokens, guesses, ResolveRound, turn
                             advancement, challenge-window bookkeeping
    log.go                   FK-free append-only stats logs
    stats.go                 leaderboard/user/card stats queries
    seed.go                  SeedAdminIfNoUsers
    youtube.go                ParseYouTubeVideoId
  static/                 embedded assets (//go:embed)
    static.go              embed.FS + SQLFiles (ORDERED game schema
                           manifest, runs AFTER the framework schema)
    sql/                   game tables/triggers under src/static/sql/
    html/                  pages/body/* (this game's own pages — NOT
                           base.html, login/users/decks/deck-access/account,
                           which are framework-owned, see below) and
                           components/tracktimeline/ (HTMX fragments)
    css/ js/ images/
  e2e_test.go              full-game integration test (real DB + websockets)
  pages_render_test.go     page-template render assertions
```

There is intentionally **no `cmd/`, `internal/`, or `pkg/`** — flat top-level
packages under `src/`. Keep it that way. Handlers that need framework data
functions import them as `gsDatabase "github.com/gerp93/gameshell-framework/database"`,
`gsApi "github.com/gerp93/gameshell-framework/api"`, etc., alongside the game
`database`/`api` packages.

## The most important architectural fact

Like timeline-trivia and unlike card-judge, **game logic here lives in Go,
not SQL**. The SQL schema (`src/static/sql/`) is just tables + a couple of
housekeeping triggers (changed-on-date, card-delete/update audit) — there are
no `SP_*`/`FN_*`/`V_*` game-rule objects. Draw-pile initialization, round
resolution, the challenge economy, turn advancement, and win detection are
all plain Go functions in `database/track-timeline.go` and `database/round.go`,
called from `api/tracktimeline`. When you change game behavior here, you are
almost always editing Go.

Schema is applied by iterating `static.SQLFiles` (in `src/static/static.go`)
on every server start via `gsBootstrap.ApplySchema`, **after** the
framework's core schema and its deck schema (`gsBootstrap.ApplyFeatureSchema`
for `Features{Decks: true}`) have run — game `CARD` FKs to the framework's
`DECK`. Order matters and is manual — tables in dependency order, then
triggers.

## Deck / card split (framework owns decks, game owns cards)

- **Decks are framework-owned**: `DECK`, `USER_ACCESS_DECK`, `AUDIT_DECK`,
  deck triggers, and the `api/deck` CRUD handlers all live in
  `gameshell-framework` and are mounted by `gsBootstrap.MountFeatures` when
  `Features.Decks` is true. This repo does not duplicate any of that.
- **Cards are game-owned**: `CARD(ID, CREATED_ON_DATE, CHANGED_ON_DATE,
  DECK_ID FK→DECK ON DELETE CASCADE, YOUTUBE_VIDEO_ID, START_OFFSET_SECONDS,
  TITLE, ARTIST, RELEASE_YEAR INT NULL, CATEGORY_ID)` + `AUDIT_CARD`, with
  CRUD in `database/card.go` and handlers in `api/card`. `RELEASE_YEAR` is
  **authored data** entered when the card is created/edited — there is no
  scraping/inference of a song's year, and a card with a NULL year is simply
  excluded from the draw pile.
- **`(DECK_ID, YOUTUBE_VIDEO_ID)` is unique** — the same song cannot be added
  to a deck twice under different metadata.
- **`OnDeckDeleting` hook** (`game/hooks.go`): MariaDB's `ON DELETE CASCADE`
  from `DECK` to `CARD` does **not** fire `CARD`'s own triggers, so the
  framework calls this hook before deleting a `DECK` and the game audits its
  own cards (`database.AuditDeckCardsAsDeleted`) in response. If you add more
  game-owned tables that FK to `DECK`, extend this hook, not a trigger on the
  framework's `DECK` table.
- **The deck detail page (`/deck/{deckId}`) follows the same split**: the
  chrome (header, Export Deck, the Edit Deck dialog, the danger-zone delete)
  is `gameshell-framework`'s `deck-detail-chrome.html`; the card table and
  create/edit-card dialogs — genuinely game-specific, since `YOUTUBE_VIDEO_ID`
  + `RELEASE_YEAR` + category don't exist in every game — are this repo's own
  `static/html/pages/body/deck-card-management.html` and
  `deck-search-controls.html`, composed with the chrome via
  `gsApiPages.ParseGameFragment` in `api/pages/pages.go`'s `Deck` handler.
  See the **body-name collision rule** below before touching either side.

## Pages owned by the framework, not this repo

Login, admin user management (`/users`), the deck list (`/decks`), the deck
password gate (`/deck/{deckId}/access`), `base.html`, and the account page's
shared chrome (theme picker, name, password, danger-zone, Win/Lose
Celebration sections) are **not** rendered by this repo — `gameshell-framework`'s
`api/pages` package (`gsApiPages`) owns the template *and* the
`http.HandlerFunc`, and `gsBootstrap.MountFeatures`/`gsBootstrap.MountStaticAssets`
mount them directly. Every page handler this repo still owns parses the
framework's `base.html` via `gsStatic.StaticFiles`, never a local copy —
there isn't one.

**Body-name collision rule**: every page body template in this repo and the
framework defines the same Go template name, `{{define "body"}}` — this only
works because exactly one body file is ever parsed per request (`parseChrome`
in `api/pages/pages.go` enforces this: one `ParseFS` call against the
framework's `base.html`, one against this repo's own body file). A composed
parse (like `Deck`'s) must never include two files that both define `"body"`
— `text/template` silently lets the second overwrite the first, with no
compile-time signal. `deck-card-management.html` and `deck-search-controls.html`
define distinctly-named blocks for exactly this reason — never rename either
to `"body"`.

**Page handlers must read the acting user from `gsApi.GetBasePageData(r).User.Id`,
never `gsApi.GetUserId(r)`.** `MiddlewareForPages` and `MiddlewareForAPIs`
populate *different* request-context keys; calling `GetUserId` from a page
handler panics with a nil interface conversion. API handlers (under
`gsApi.MiddlewareForAPIs`) are the reverse — they use `gsApi.GetUserId(r)`.

## The token/challenge economy

This is the one place this game's rules genuinely differ from timeline-trivia,
which just passes a missed card to the next player with no cost. Here:

- Placing correctly on your own turn wins the card outright — no challenge
  possible, since nobody else has anything to react to yet.
- Placing incorrectly opens a challenge window (`ROUND_PHASE = 'challenge'`,
  `CHALLENGE_OPENED_ON_DATE` stamped) **only if** at least one other active
  player holds a token and has not already placed — see
  `database.ChallengersOutstanding`. If nobody can act, the round resolves
  immediately instead of opening a window nobody can use.
- A challenge costs exactly one token (`database.Challenge` in
  `api/tracktimeline/round.go` deducts it) and commits a placement the same
  way the original guess did (`TRACK_TIMELINE_PLACEMENT.IS_CHALLENGE = 1`).
  You cannot challenge your own placement.
- `ResolveRound` (`database/round.go`) judges the original placement first,
  then challengers **in the order they committed** (`ORDER BY
  CREATED_ON_DATE ASC`). The first placement that is correct wins the card;
  everyone else's attempt is still judged and logged for stats, but only one
  card changes hands per round.
- Free-form artist/title guesses (`SubmitGuess`) are judged **as they
  arrive, not at reveal** — a token earned this round can immediately fund a
  challenge on the same round. This is deliberate tension, not a bug: it is
  what makes "did you actually know the song, or are you guessing where to
  place it" two separate skills that both pay off.
- One guess and one placement/challenge per player per round — enforced by
  `HasGuessed` and the placement table's unique constraint, respectively.

## Metadata hiding

`database.CurrentCard` has no `Title`/`Artist`/`ReleaseYear` field at all —
that is the answer, and a template or JSON payload that cannot reach a field
cannot leak it. `database.CurrentCardAnswer` is a separate type embedding
`CurrentCard` and is only ever fetched once `RoundPhase == PhaseReveal`, or
server-side where the answer is being *compared* rather than sent (e.g.
`ResolveRound`, `SubmitGuess`'s judging). Keep this split when adding new
code that touches the card in play — do not add title/artist/year fields to
`CurrentCard` itself, and do not fetch `CurrentCardAnswer` outside the reveal
phase or a server-side comparison.

The `YouTubeVideoId` field on `CurrentCard` is the one exception, and it has
to be: it is what plays the audio. A player with developer tools open can
look it up. This game treats that the same way its tabletop ancestor does —
as something nobody who wants to play will bother doing — not as a security
boundary. See "Real-time" below for the same caveat applied to the DOM.

## Go conventions (match these exactly)

- **Package naming:** subpackages under `api/` are named `api<Thing>` even
  though the directory is lowercase — package `apiCard` in `api/card/`,
  `apiTrackTimeline` in `api/tracktimeline/`, `apiPages` in `api/pages/`.
  Top-level packages (`database`, `game`, `static`, `guess`) match their
  directory. `gofmt`/tabs.
- **Handlers** have the shape `func Name(w http.ResponseWriter, r *http.Request)`
  and are wired in `main.go` with Go 1.22 method+pattern routes
  (`http.Handle("POST /api/...", gsApi.MiddlewareForAPIs(http.HandlerFunc(...)))`).
  Gameplay handlers share one `loadContext(w, r) (gameContext, bool)` helper
  (`api/tracktimeline/track-timeline.go`) that resolves the lobby, its game,
  and the acting player in one place, writing its own error response and
  returning `false` on any problem — every gameplay handler starts with
  `ctx, ok := loadContext(w, r); if !ok { return }`.
- **Form/param parsing** uses `r.ParseForm()` plus `r.FormValue`/`r.Form[...]`
  reads, not a decode library.
- **Responses are plain text**, written directly — no JSON envelope, except
  the one structured websocket payload (`result:`, see below):
  ```go
  w.WriteHeader(http.StatusBadRequest)
  _, _ = w.Write([]byte("No song is in play."))
  ```
  Messages are human-readable sentences, capitalized, ending with a period.
  The `_, _ =` discard on `Write` is deliberate and consistent — keep it.
- **DB layer:** raw SQL strings passed to `query`/`execute`
  (`database/database.go`'s thin wrappers over `gsDatabase.Query`/`Execute`).
  Multi-line SQL uses backtick literals; one-liners use double quotes. Read
  results row-by-row with `defer rows.Close()` then `rows.Scan(...)`. On scan
  error the pattern is `log.Println(err); return ..., errors.New("failed to
  scan row in query results")`. Structs mirror table columns (PascalCase
  fields, `sql.Null*`/`uuid.NullUUID` for nullables). No ORM, no query
  builder — do not introduce one.
- **IDs** are `uuid.UUID` (`github.com/google/uuid`), generated with
  `uuid.NewUUID()` in Go or `UUID()` in SQL.
- **Config** is environment variables via `os.Getenv`, all prefixed
  `TRACK_TIMELINE_` (`_SQL_HOST/_SQL_DATABASE/_SQL_USER/_SQL_PASSWORD`,
  `_PORT`, `_LOG_FILE`, `_CERT_FILE`, `_KEY_FILE`, `_ADMIN_PASSWORD`). No
  config files or libraries.
- **The `guess` package is a seam, not a game concern.** `Judge` takes three
  strings and returns two booleans — it knows nothing about lobbies, rounds,
  or tokens. `Adjudicate` (`guess/guess.go`) wraps whatever `Judge` is
  configured in a 5-second timeout and **always falls back to the local
  `Normalized` implementation on any error** — a missing API key, a timeout,
  a rate limit degrades judging quality instead of stalling the round for
  the whole lobby. Preserve that fallback if you touch this file. See
  `src/guess/README.md` for how a model-backed judge plugs in (deliberately
  left unimplemented — see that file's header for why).

## SQL conventions (match these exactly)

- **Uppercase everything** — keywords AND identifiers (table/column names).
- **One database object per file**, named after the object, using prefixes:
  `TR_` trigger, `AUDIT_` history table, `LOG_` append-only stats table.
  (No `SP_`/`FN_`/`V_` objects exist in this repo today — see "most
  important architectural fact" above.)
- Tables use `CREATE TABLE IF NOT EXISTS`; triggers use `CREATE OR REPLACE`
  so re-running the manifest is idempotent.
- **Format with the repo's formatter**, not by hand:
  `src/static/sql/sqlfmt.sh` runs `sqlfmt --newlines --upper --spaces 4
  --comment-pre-space` over every `*.sql`. Run it after editing SQL.
- After adding/removing a SQL file, update `SQLFiles` in `src/static/static.go`.
- **The `TRACK_TIMELINE_LOG_*` tables are deliberately FK-free**, same reason
  as timeline-trivia's flagged-card/stats tables: a lobby (and everything
  hanging off it) is deleted when the last websocket client disconnects
  (framework behavior), but stats have to outlive that. A card's stats page
  (`GetCardStats`) still resolves after the card itself is deleted — it
  shows "(deleted song)" rather than erroring. Do not add a foreign key to
  any `LOG_` table without removing this guarantee first.

## Real-time (websocket) pattern

Messages over the socket are **short control strings, not structured
payloads**, except `result:` and `song:`, whose payloads are JSON. Control
strings: `refresh`, `reload`, `result:<json>`, `song:<json>`, `songStop`,
`status:<text>`, `chat:...`, `alert:...`, `lobbyMessage:<text>`, `kick`. The
server broadcasts a hint and the browser (`src/static/js/track-timeline.js`)
reacts by re-fetching the relevant HTML fragment via `htmx.ajax`/`fetch` from
`/api/track-timeline/{lobbyId}/...` routes, or by driving the YouTube IFrame
player. HTML is never pushed over the socket. Chat message rendering is
**shared with card-judge and timeline-trivia** via `gameshell-framework`'s
`static/js/chat.js` (`window.gsChat`), mounted at `/gs/js/chat.js` — do not
reintroduce a local copy.

**`result:` drives the reveal popup and bottom-of-screen status line for
every client, including whoever acted; `status:` is the same idea without a
popup**, for non-reveal events like "a challenge window opened" or "N more
players could still challenge." Handlers that need to tell only the *acting*
player something private (e.g. what part of a guess was right) use
`gsWebsocket.PlayerBroadcast(playerId, "alert:"+text)` instead — the lobby
only ever hears that a guess scored, never what it contained
(`SubmitGuess` in `api/tracktimeline/round.go`).

**The song stops (`songStop`) the instant a placement locks in.** Leaving it
playing through the challenge window would give challengers more listening
time than the player on turn got. Playback itself is started by the player on
turn calling `PlaySong` rather than automatically on the previous round's
reveal — that gives everyone time to read the answer, and a song never starts
underneath a popup.

**The YouTube player is hidden with off-screen CSS, not `display:none`** —
the IFrame API needs real element dimensions to attach. This is a UI
convenience, explicitly not a security boundary; see "Metadata hiding" above.

Note that `reload` (used after `StartGame`/`ResetGame`/a game-over) waits
~500ms before refreshing rather than doing a full page navigation: a
`location.reload()` drops the websocket connection, and if this player is the
only client, the framework deletes the (now-empty) lobby before the reload
finishes, destroying the game that was just started/reset.

## HTML conventions

**Navigation is always a real `<a href>`.** Anything that takes the user to
another page must be wrapped in an anchor — never
`onclick="location.href='...'"`, and never a JavaScript click-interceptor.
Only a real anchor gets ctrl/cmd-click and middle-click to open a new tab,
right-click → "Open in new tab", the hover URL preview, and link semantics
for screen readers; a script cannot reproduce all of that, and this is the
same convention timeline-trivia and card-judge follow.

```html
<a href="/decks"><button>Card Decks</button></a>
<a href="/track-timeline/{{.Id}}"><button class="btn-small">Join</button></a>
<a href="/account" class="no-style">
    <div class="top-bar-menu-link">Account <i class="bi bi-gear"></i></div>
</a>
```

`a.no-style` (`static/css/global.css`) strips the anchor's underline/color so
a wrapped menu row looks unchanged. Buttons that perform an *action* rather
than navigate (`hx-post`/`hx-put`/`hx-delete`, opening a `<dialog>`) stay
plain buttons. External links additionally take `target="_blank"`.

## Build / run / verify

- Build: `cd src && go build ./...`.
- Run: needs a MariaDB reachable via the `TRACK_TIMELINE_SQL_*` env vars;
  create the DB once with `src/static/sql/setup.sql`, then the server applies
  the rest of the schema (framework, then game) on startup. Serves on `:2016`
  (or `TRACK_TIMELINE_PORT`).
- Docker: root `Dockerfile` builds and runs the binary.
- Versioning: `version_bump.sh {major|minor|patch}` (own version, tracked
  separately from `gameshell-framework`, card-judge, and timeline-trivia).
- Deployment tooling lives in the separate `gameshell-deploy` repo; this repo
  only keeps a `backups/` directory for it to use.
- `src/e2e_test.go` drives the real HTTP handlers against a real database
  with real websocket clients (sessions minted via `gsAuth.SetUserId`, valid
  in-process since the framework's signing secret is per-process) — it's the
  main regression net for the challenge economy, token spending, guess
  judging, skip/timeout edge cases, and the win/reset/reshuffle guarantee. It
  refuses to run unless `TRACK_TIMELINE_SQL_DATABASE` starts with `tt_e2e`,
  since it seeds and mutates freely:
  `TRACK_TIMELINE_SQL_DATABASE=tt_e2e go test ./...` against a throwaway
  database (create it first; the schema runner creates tables but not the
  database itself). `pages_render_test.go` (same `tt_e2e` requirement)
  renders every page template and asserts on real content — these handlers
  discard `ExecuteTemplate`'s error, so a template/data mismatch fails
  silently (a 200 with a truncated body) rather than with a visible error; a
  status-code-only check would miss that. `database/round_test.go` and
  `guess/normalized_test.go` cover pure functions with no DB needed.
- Still **verify UI/visual changes by running the app and playing through the
  affected flow** (create a lobby, join with two players, start a song, place
  correctly and incorrectly, challenge a placement, submit a guess, confirm a
  win) — the automated tests exercise the Go handlers and JS logic, not
  layout, color contrast, or animation, and nothing automated verifies actual
  YouTube playback.

## Known quirks (preserve unless explicitly changing)

- The full SQL schema (framework, then game) re-runs on every startup
  (idempotent by design).
- The lobby is **deleted when its last websocket client disconnects**
  (framework `websocket/hub.go`) — this is why the `LOG_*` stats tables are
  FK-free (see "SQL conventions" above).
- The auth signing secret is process-random (framework `auth/cookie.go`), so
  sessions do not survive a restart and cannot be shared across instances —
  after restarting locally you'll need to log back in.
- A card with `RELEASE_YEAR IS NULL` is authored-but-incomplete; it's
  silently excluded from every draw pile rather than erroring.
- `ResetGame` deliberately does **not** clear `TRACK_TIMELINE_PLAYER_ORDER`.
  `ShufflePlayerOrder` (called from `StartGame`) needs the still-there rows
  as the "previous order" baseline so it can guarantee the new shuffle
  differs from the last game's — not just probably differ, which a fresh
  `rand.Shuffle` alone only gives you (1/N! chance of reproducing the same
  order by pure luck). If you re-add a clear-on-reset, that guarantee
  silently degrades back to probabilistic.
- `guess.SetJudge` is never called anywhere in this repo — the local
  `Normalized` judge is the only one wired in. This is intentional (see
  `src/guess/README.md`), not an oversight to "fix" by wiring up a stub.
