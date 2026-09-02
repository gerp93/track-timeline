package database

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"
	"os"

	gsDatabase "github.com/gerp93/gameshell-framework/database"
)

// SeedAdminIfNoUsers creates one approved admin account, but only when the USER
// table is completely empty — i.e. on a genuinely fresh deployment. It is a
// no-op forever after, so it is safe to run on every startup.
//
// The name comes from TRACK_TIMELINE_ADMIN_USER (default "admin"). The password
// comes from TRACK_TIMELINE_ADMIN_PASSWORD; when that is unset a random one is
// generated and written to the log exactly once, at creation time.
//
// Deliberately unlike timeline-trivia, which seeds a fixed "default"/"password"
// account: a well-known credential that ships with the binary is a standing
// invitation on anything reachable from a network, and this game is meant to be
// deployed. Nothing here is secret enough to be worth a hardcoded password.
func SeedAdminIfNoUsers() error {
	sqlString := "SELECT COUNT(*) FROM USER"
	rows, err := query(sqlString)
	if err != nil {
		log.Println(err)
		return errors.New("failed to check whether any users exist")
	}
	defer rows.Close()

	var count int
	if !rows.Next() {
		return errors.New("failed to query user count")
	}
	if err := rows.Scan(&count); err != nil {
		log.Println(err)
		return errors.New("failed to scan row in query results")
	}
	if count > 0 {
		return nil
	}

	name := os.Getenv("TRACK_TIMELINE_ADMIN_USER")
	if name == "" {
		name = "admin"
	}

	password := os.Getenv("TRACK_TIMELINE_ADMIN_PASSWORD")
	generated := false
	if password == "" {
		password, err = randomPassword()
		if err != nil {
			return err
		}
		generated = true
	}

	if err := gsDatabase.CreateUser(name, password, true); err != nil {
		return err
	}

	userId, err := gsDatabase.GetUserIdByName(name)
	if err != nil {
		return err
	}
	if err := gsDatabase.SetUserIsAdmin(userId, true); err != nil {
		return err
	}

	if generated {
		log.Printf("created initial admin user %q with generated password: %s", name, password)
		log.Println("log in and change this password, or set TRACK_TIMELINE_ADMIN_PASSWORD before first start")
	} else {
		log.Printf("created initial admin user %q from TRACK_TIMELINE_ADMIN_PASSWORD", name)
	}

	return nil
}

func randomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		log.Println(err)
		return "", errors.New("failed to generate a random password")
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
