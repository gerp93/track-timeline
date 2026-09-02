# Track Timeline

Version: 0.1.0

A music chronology party game. Players hear a song and place it into their
personal timeline by release year. A correct placement grows the timeline; an
incorrect one lets an opponent steal it. First player to reach the configured
number of cards wins.

Correctly naming a song's artist and title earns a token, and tokens are spent
to challenge another player's placement — so knowing the music pays off twice.

Built on [`gameshell-framework`](https://github.com/gerp93/gameshell-framework),
the same platform behind
[`card-judge`](https://github.com/gerp93/card-judge) and
[`timeline-trivia`](https://github.com/gerp93/timeline-trivia). This repo holds
only the game; the framework owns accounts, lobbies, decks, chat, and the
websocket hub.

## Environment Variables

```
TRACK_TIMELINE_SQL_HOST         required   MariaDB host
TRACK_TIMELINE_SQL_DATABASE     required   database name
TRACK_TIMELINE_SQL_USER         required   database user
TRACK_TIMELINE_SQL_PASSWORD     required   database password

TRACK_TIMELINE_PORT             optional   defaults to 2016
TRACK_TIMELINE_LOG_FILE         optional   defaults to stdout
TRACK_TIMELINE_CERT_FILE        optional   enables HTTPS when set with _KEY_FILE
TRACK_TIMELINE_KEY_FILE         optional   enables HTTPS when set with _CERT_FILE
TRACK_TIMELINE_ADMIN_PASSWORD   optional   sets the seeded admin's password on
                                            first start (only when the USER
                                            table is empty); a random one is
                                            generated and logged once if unset
```

Create the database once with `src/static/sql/setup.sql`; the server applies the
rest of the schema (framework first, then game) on every startup.

## Judging guesses

Free-form artist/title guesses are judged by a small `guess.Judge` interface
(`src/guess/`). A local, dependency-free implementation (`Normalized`) ships
by default and is always the fallback if a configured judge errors or times
out. See [`src/guess/README.md`](src/guess/README.md) for how to plug in a
Claude-backed judge — that piece is intentionally left unwired so it can be
added as a follow-up.

## Build and Run

```sh
cd src && go build ./...
```

Needs a MariaDB reachable via the `TRACK_TIMELINE_SQL_*` env vars above.

The root `Dockerfile` builds and runs the binary. Deployment tooling lives in the
separate `gameshell-deploy` repo.

## Tests

`src/e2e_test.go` and `src/pages_render_test.go` drive real HTTP handlers,
real websocket clients, and real page templates against a real database —
they refuse to run unless `TRACK_TIMELINE_SQL_DATABASE` starts with `tt_e2e`,
since they seed and mutate freely:

```sh
TRACK_TIMELINE_SQL_DATABASE=tt_e2e go test ./...
```

against a throwaway database (create it first; the schema runner creates
tables but not the database itself). `src/database/*_test.go` and
`src/guess/*_test.go` are pure-function tests that need no database.

## Versioning

`./version_bump.sh {major|minor|patch}` updates the `Version:` line above and
prints the git commands to tag the release.
