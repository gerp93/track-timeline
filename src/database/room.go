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

// RoomSummary is one of a user's rooms as the Lobbies page lists it, so someone
// who left or lost their screen or phone can find the way back in.
type RoomSummary struct {
	Code       string
	Name       string
	GameStatus string
	// IsHost: this user created the room and may open its host screen and
	// delete it. IsPlayer: this user has a seat in it (they may be both).
	IsHost   bool
	IsPlayer bool
}

// GetRoomsForUser lists the rooms a user created or has sat down in, newest
// first. Rooms never show in the normal lobby list (they are joined by code,
// not by browsing), so this is the way back for a host whose screen closed or
// a player whose phone page was lost.
func GetRoomsForUser(userId uuid.UUID) ([]RoomSummary, error) {
	rows, err := query(`
		SELECT
			R.CODE,
			L.NAME,
			G.GAME_STATUS,
			R.CREATOR_USER_ID = ? AS IS_HOST,
			EXISTS(SELECT 1 FROM PLAYER AS P WHERE P.LOBBY_ID = R.LOBBY_ID AND P.USER_ID = ?) AS IS_PLAYER
		FROM TRACK_TIMELINE_ROOM AS R
			INNER JOIN LOBBY AS L ON L.ID = R.LOBBY_ID
			INNER JOIN TRACK_TIMELINE_GAME AS G ON G.LOBBY_ID = R.LOBBY_ID
		WHERE R.CREATOR_USER_ID = ?
			OR EXISTS(SELECT 1 FROM PLAYER AS P WHERE P.LOBBY_ID = R.LOBBY_ID AND P.USER_ID = ?)
		ORDER BY R.CREATED_ON_DATE DESC
	`, userId, userId, userId, userId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rooms := []RoomSummary{}
	for rows.Next() {
		var room RoomSummary
		if err := rows.Scan(&room.Code, &room.Name, &room.GameStatus, &room.IsHost, &room.IsPlayer); err != nil {
			return nil, err
		}
		// The lobby name carries the code ("Living Room [AB12]") so hosts can
		// tell rooms apart elsewhere; this list shows the code in its own column.
		room.Name = strings.TrimSuffix(room.Name, " ["+room.Code+"]")
		rooms = append(rooms, room)
	}
	return rooms, nil
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

// GuestDisplayName is the nickname a guest typed: GuestUserName's inverse,
// dropping the "·code" suffix that keeps USER.NAME unique. Real account names
// pass through untouched.
func GuestDisplayName(userName string) string {
	name, _, _ := strings.Cut(userName, "·")
	return name
}

// GuestUserName builds a unique USER.NAME for a room guest seat. The visible
// nickname is the part before the middle dot; the suffix keeps USER.NAME unique.
func GuestUserName(displayName string, roomCode string) string {
	return fmt.Sprintf("%s·%s", displayName, strings.ToLower(roomCode))
}
