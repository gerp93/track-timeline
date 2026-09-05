#!/usr/bin/env bash
# Claude-sandbox tooling, not part of the shipped app. See dev/claude-sandbox/README.md.
#
# TestTrackTimelineEndToEnd and friends create fixed-name users/decks/lobbies
# (e.g. "e2e_alice") and don't clean up after themselves, so a second run
# against the same tt_e2e database fails on a duplicate-key error rather than
# a real test failure. Run this before every `go test ./...` invocation, not
# just the first.
#
# Usage:
#   bash dev/claude-sandbox/reset-e2e-db.sh

set -euo pipefail

mysql -uroot -e "DROP DATABASE IF EXISTS tt_e2e; CREATE DATABASE tt_e2e;"
echo "tt_e2e reset."
