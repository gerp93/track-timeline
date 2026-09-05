#!/usr/bin/env bash
# Claude-sandbox tooling, not part of the shipped app. See dev/claude-sandbox/README.md.
#
# Stands up a local MariaDB inside an ephemeral Claude Code cloud sandbox
# (no systemd, no Docker registry access) so the real server binary and the
# full e2e test suite can run against a real database instead of being
# skipped. Idempotent: safe to re-run in the same container.
#
# Usage:
#   bash dev/claude-sandbox/setup-mariadb.sh
#
# After it succeeds, export these before running the server or go test:
#   export TRACK_TIMELINE_SQL_HOST=127.0.0.1
#   export TRACK_TIMELINE_SQL_USER=tt_app
#   export TRACK_TIMELINE_SQL_PASSWORD='PlaytestDbPass123!'
#   export TRACK_TIMELINE_SQL_DATABASE=track_timeline_dev   # manual/Playwright runs
#   # or:
#   export TRACK_TIMELINE_SQL_DATABASE=tt_e2e                # `go test ./...`
#
# Why not Docker: the sandbox's outbound network policy blocks the Docker Hub
# registry (a `docker run mariadb:...` 403s), but archive.ubuntu.com is
# reachable, so `apt-get install mariadb-server` works fine.
#
# Why a dedicated tt_app user instead of root: MariaDB's `root`@`localhost`
# account uses the unix_socket/auth_socket plugin by default, which only
# authenticates local socket connections as the matching OS user -- it does
# not accept a password over TCP. gameshell-framework's DB layer always
# connects over TCP (`user:pass@tcp(host:3306)/db`, see
# gameshell-framework/database/database.go), so a plain password account is
# required regardless of who runs this. (Don't add `IDENTIFIED WITH
# mysql_native_password BY '...'` to the CREATE USER below -- that MySQL
# syntax is a syntax error on this MariaDB version; plain `IDENTIFIED BY`
# already picks a TCP-capable plugin.)

set -euo pipefail

DB_USER="tt_app"
DB_PASSWORD="PlaytestDbPass123!"
DEV_DB="track_timeline_dev"
E2E_DB="tt_e2e"

if ! command -v mariadbd >/dev/null 2>&1; then
	echo "installing mariadb-server..."
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq
	apt-get install -qq -y mariadb-server >/dev/null
fi

mkdir -p /run/mysqld
chown mysql:mysql /run/mysqld

if ! mysqladmin ping >/dev/null 2>&1; then
	echo "starting mariadbd..."
	nohup mariadbd-safe --user=mysql >/tmp/mariadb.log 2>&1 &
	for _ in $(seq 1 30); do
		mysqladmin ping >/dev/null 2>&1 && break
		sleep 1
	done
	mysqladmin ping >/dev/null 2>&1 || {
		echo "mariadbd did not come up; see /tmp/mariadb.log" >&2
		exit 1
	}
else
	echo "mariadbd is already running"
fi

echo "creating databases and app user..."
mysql -uroot <<SQL
CREATE DATABASE IF NOT EXISTS ${DEV_DB};
CREATE DATABASE IF NOT EXISTS ${E2E_DB};

CREATE USER IF NOT EXISTS '${DB_USER}'@'localhost' IDENTIFIED BY '${DB_PASSWORD}';
CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD}';
-- CREATE USER IF NOT EXISTS is a no-op on an existing account, so a re-run
-- against a container that already has tt_app from a prior session (data
-- dir persisted, mysqld restarted) would otherwise leave a stale/unknown
-- password in place -- reset it explicitly every run.
SET PASSWORD FOR '${DB_USER}'@'localhost' = PASSWORD('${DB_PASSWORD}');
SET PASSWORD FOR '${DB_USER}'@'127.0.0.1' = PASSWORD('${DB_PASSWORD}');

-- Global, not per-database: the framework/game schema creates triggers
-- (TR_*), and MariaDB requires the SUPER privilege (or TRIGGER +
-- SUPER-gated definer rights, depending on log_bin_trust_function_creators)
-- to create one -- a per-database ALL PRIVILEGES grant is not enough and
-- schema application fails with "Access denied; you need (at least one of)
-- the SUPER privilege(s)".
GRANT ALL PRIVILEGES ON *.* TO '${DB_USER}'@'localhost' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO '${DB_USER}'@'127.0.0.1' WITH GRANT OPTION;
FLUSH PRIVILEGES;
SQL

echo "verifying TCP login as ${DB_USER}..."
mysql --protocol=TCP -h127.0.0.1 -P3306 -u"${DB_USER}" -p"${DB_PASSWORD}" -e "SELECT 1;" >/dev/null

cat <<EOF

MariaDB is up. Export these before running the server or go test:

  export TRACK_TIMELINE_SQL_HOST=127.0.0.1
  export TRACK_TIMELINE_SQL_USER=${DB_USER}
  export TRACK_TIMELINE_SQL_PASSWORD='${DB_PASSWORD}'
  export TRACK_TIMELINE_SQL_DATABASE=${DEV_DB}   # manual/Playwright runs
  # or:
  export TRACK_TIMELINE_SQL_DATABASE=${E2E_DB}    # \`go test ./...\` (see reset-e2e-db.sh first)
EOF
