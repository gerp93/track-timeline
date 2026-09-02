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
```

Create the database once with `src/static/sql/setup.sql`; the server applies the
rest of the schema (framework first, then game) on every startup.

## Build and Run

```sh
cd src && go build ./...
```

The root `Dockerfile` builds and runs the binary. Deployment tooling lives in the
separate `gameshell-deploy` repo.

## Versioning

`./version_bump.sh {major|minor|patch}` updates the `Version:` line above and
prints the git commands to tag the release.
