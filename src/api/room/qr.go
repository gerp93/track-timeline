package apiRoom

import (
	"net/http"
	"strings"

	"github.com/skip2/go-qrcode"

	"github.com/gerp93/track-timeline/database"
)

// joinURL is the address a phone opens to sit down in a room, built from how
// this request reached the server so it works behind a proxy or on a LAN.
func joinURL(r *http.Request, code string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/room/" + code
}

// QRCode serves a QR code of the room's join link, for the host display to
// show so phones can scan their way in instead of typing the address. The join
// link is public by design (anyone with the code can sit down), so this needs
// no host token.
func QRCode(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	room, err := database.GetRoomByCode(code)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No room with that code."))
		return
	}

	png, err := qrcode.Encode(joinURL(r, room.Code), qrcode.Medium, 512)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Failed to draw the QR code."))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}
