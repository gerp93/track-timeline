package database

import (
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
)

// Lobby mirrors the framework's LOBBY table.
//
// The framework owns the table and the write path (CreateLobby, SetLobbyName,
// SetLobbyMessage) but exports no reader and no struct, so the game page has to
// read it directly. Kept to exactly the columns that exist, so it stays honest
// if the framework's schema moves.
type Lobby struct {
	Id            uuid.UUID
	CreatedOnDate time.Time
	Name          string
	Message       sql.NullString
	HasPassword   bool
}

// GetLobby returns one lobby, or the zero Lobby with a nil error when there is
// no such row — the same shape the framework's own getters use, so callers
// check Id against uuid.Nil.
func GetLobby(lobbyId uuid.UUID) (Lobby, error) {
	var lobby Lobby

	sqlString := `
		SELECT ID, CREATED_ON_DATE, NAME, MESSAGE, PASSWORD_HASH IS NOT NULL
		FROM LOBBY
		WHERE ID = ?
	`
	rows, err := query(sqlString, lobbyId)
	if err != nil {
		return lobby, err
	}
	defer rows.Close()

	for rows.Next() {
		if err := rows.Scan(&lobby.Id, &lobby.CreatedOnDate, &lobby.Name, &lobby.Message, &lobby.HasPassword); err != nil {
			log.Println(err)
			return lobby, errors.New("failed to scan row in query results")
		}
	}

	return lobby, nil
}
