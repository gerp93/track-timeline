package apiRoom

import (
	"log"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/gerp93/track-timeline/database"
)

// Role identifies how a room websocket client participates.
const (
	RoleHost = "host"
	RoleSeat = "seat"
)

type client struct {
	hub    *hub
	conn   *websocket.Conn
	send   chan []byte
	role   string
	userId uuid.UUID
}

type hub struct {
	lobbyId    uuid.UUID
	clients    map[*client]bool
	broadcast  chan []byte
	register   chan *client
	unregister chan *client
}

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
	hubsMu sync.Mutex
	hubs   = map[uuid.UUID]*hub{}
)

func getHub(lobbyId uuid.UUID) *hub {
	hubsMu.Lock()
	defer hubsMu.Unlock()
	if h, ok := hubs[lobbyId]; ok {
		return h
	}
	h := &hub{
		lobbyId:    lobbyId,
		clients:    make(map[*client]bool),
		broadcast:  make(chan []byte, 16),
		register:   make(chan *client),
		unregister: make(chan *client),
	}
	hubs[lobbyId] = h
	go h.run()
	return h
}

func (h *hub) run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
			if c.role == RoleHost {
				if err := database.SetRoomPaused(h.lobbyId, false); err != nil {
					log.Println(err)
				}
				h.sendAll([]byte("resumed"))
				h.sendAll([]byte("log:Host display connected"))
				h.sendAll([]byte("refresh"))
			}
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			if c.role == RoleHost && !h.hasHost() {
				if err := database.SetRoomPaused(h.lobbyId, true); err != nil {
					log.Println(err)
				}
				h.sendAll([]byte("paused"))
				h.sendAll([]byte("log:Host display disconnected — game paused"))
				h.sendAll([]byte("refresh"))
			}
			if len(h.clients) == 0 {
				hubsMu.Lock()
				delete(hubs, h.lobbyId)
				hubsMu.Unlock()
				return
			}
		case message := <-h.broadcast:
			h.sendAll(message)
		}
	}
}

func (h *hub) hasHost() bool {
	for c := range h.clients {
		if c.role == RoleHost {
			return true
		}
	}
	return false
}

func (h *hub) sendAll(message []byte) {
	for c := range h.clients {
		select {
		case c.send <- message:
		default:
			close(c.send)
			delete(h.clients, c)
		}
	}
}

// Broadcast sends a control string to every room client for this lobby.
// No-op when no room hub is running (remote lobbies, or room with nobody connected).
func Broadcast(lobbyId uuid.UUID, message string) {
	hubsMu.Lock()
	h, ok := hubs[lobbyId]
	hubsMu.Unlock()
	if !ok {
		return
	}
	select {
	case h.broadcast <- []byte(message):
	default:
		log.Println("room hub broadcast dropped for", lobbyId)
	}
}

// MirrorBroadcast copies a remote-style gameplay message into the room hub when
// the lobby is room-mode. Remote LobbyBroadcast stays as-is for online play.
func MirrorBroadcast(lobbyId uuid.UUID, message string) {
	isRoom, err := database.LobbyIsRoom(lobbyId)
	if err != nil || !isRoom {
		return
	}
	Broadcast(lobbyId, message)
}

func (c *client) writePump() {
	defer c.conn.Close()
	for message := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return
		}
	}
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
		// Room clients are display/controllers; they do not send chat over the
		// socket in v1 (TV log is server-authored only).
	}
}
