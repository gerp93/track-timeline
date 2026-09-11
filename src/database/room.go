package database

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// Room is a same-room (TV + phones) session layered on top of a normal lobby
// and TRACK_TIMELINE_GAME. The host is seatless: they hold HOST_TOKEN, never a
// PLAYER row. Phone seats are ordinary PLAYER rows (account or synthetic guest
// USER). Remote lobby search excludes any lobby with a matching room row.
type Room struct {
	Id            uuid.UUID
	LobbyId       uuid.UUID
	Code          string
	HostToken     string
	CreatorUserId uuid.UUID
	IsPaused      bool
	Name          string
}

const roomCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewRoomCode returns a short, unambiguous join code (no 0/O/1/I).
func NewRoomCode() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, 4)
	for i, b := range buf {
		out[i] = roomCodeAlphabet[int(b)%len(roomCodeAlphabet)]
	}
	return string(out), nil
}

// NewHostToken returns a 32-byte hex token the host display presents as a cookie.
func NewHostToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// CreateRoom attaches room-mode metadata to an existing lobby+game.
func CreateRoom(lobbyId uuid.UUID, creatorUserId uuid.UUID, code string, hostToken string) (uuid.UUID, error) {
	id := uuid.New()
	err := execute(`
		INSERT INTO TRACK_TIMELINE_ROOM(ID, LOBBY_ID, CODE, HOST_TOKEN, CREATOR_USER_ID)
		VALUES (?, ?, ?, ?, ?)
	`, id, lobbyId, strings.ToUpper(strings.TrimSpace(code)), hostToken, creatorUserId)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func scanRoom(scanner interface {
	Scan(dest ...any) error
}) (Room, error) {
	var room Room
	err := scanner.Scan(
		&room.Id,
		&room.LobbyId,
		&room.Code,
		&room.HostToken,
		&room.CreatorUserId,
		&room.IsPaused,
		&room.Name,
	)
	return room, err
}

const roomSelect = `
	SELECT
		R.ID,
		R.LOBBY_ID,
		R.CODE,
		R.HOST_TOKEN,
		R.CREATOR_USER_ID,
		R.IS_PAUSED,
		L.NAME
	FROM TRACK_TIMELINE_ROOM AS R
		INNER JOIN LOBBY AS L ON L.ID = R.LOBBY_ID
`

// GetRoomByCode looks up a room by its public join code.
func GetRoomByCode(code string) (Room, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	rows, err := query(roomSelect+` WHERE R.CODE = ?`, code)
	if err != nil {
		return Room{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Room{}, sql.ErrNoRows
	}
	room, err := scanRoom(rows)
	if err != nil {
		return Room{}, err
	}
	return room, nil
}

// GetRoomByLobbyId looks up room metadata for a lobby, if it is room-mode.
func GetRoomByLobbyId(lobbyId uuid.UUID) (Room, error) {
	rows, err := query(roomSelect+` WHERE R.LOBBY_ID = ?`, lobbyId)
	if err != nil {
		return Room{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Room{}, sql.ErrNoRows
	}
	return scanRoom(rows)
}

// LobbyIsRoom reports whether a lobby is a room-mode session.
func LobbyIsRoom(lobbyId uuid.UUID) (bool, error) {
	rows, err := query(`SELECT 1 FROM TRACK_TIMELINE_ROOM WHERE LOBBY_ID = ?`, lobbyId)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), nil
}

// SetRoomPaused freezes or unfreezes room gameplay (host disconnect / reconnect).
func SetRoomPaused(lobbyId uuid.UUID, paused bool) error {
	return execute(`UPDATE TRACK_TIMELINE_ROOM SET IS_PAUSED = ? WHERE LOBBY_ID = ?`, paused, lobbyId)
}

// ValidateGuestDisplayName bounds and sanitizes a couch-guest nickname.
func ValidateGuestDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("enter a display name")
	}
	if utf8Len := len([]rune(name)); utf8Len > 24 {
		return "", errors.New("display name must be 24 characters or fewer")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("display name has invalid characters")
		}
	}
	return name, nil
}

// GuestUserName builds a unique USER.NAME for a room guest seat. The visible
// nickname is the part before the middle dot; the suffix keeps USER.NAME unique.
func GuestUserName(displayName string, roomCode string) string {
	return fmt.Sprintf("%s·%s", displayName, strings.ToLower(roomCode))
}
