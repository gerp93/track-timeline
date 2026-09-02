package database

import (
	"database/sql"

	gsDatabase "github.com/gerp93/gameshell-framework/database"
)

// query and execute are thin passthroughs to the framework's data layer. They
// exist so this package's call sites read as plain `query(sqlString, args...)`
// rather than repeating the gsDatabase prefix on every statement, and so a
// future change to how the game talks to the database has one place to land.

func query(sqlString string, params ...any) (*sql.Rows, error) {
	return gsDatabase.Query(sqlString, params...)
}

func execute(sqlString string, params ...any) error {
	return gsDatabase.Execute(sqlString, params...)
}
