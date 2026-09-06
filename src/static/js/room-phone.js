let roomPhoneCode = null;
let roomPhoneLobbyId = null;
let roomPhoneConn = null;

function initRoomPhone(code, lobbyId) {
    roomPhoneCode = code;
    roomPhoneLobbyId = lobbyId;
    connectRoomPhoneSocket();
}

function connectRoomPhoneSocket() {
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    roomPhoneConn = new WebSocket(protocol + "//" + location.host + "/ws/room/" + roomPhoneCode + "?role=seat");
    roomPhoneConn.onmessage = (e) => roomPhoneOnMessage(e.data);
    roomPhoneConn.onclose = () => setTimeout(connectRoomPhoneSocket, 1500);
}

function roomPhoneOnMessage(message) {
    if (message === "refresh") {
        document.body.dispatchEvent(new Event("room-refresh"));
        return;
    }
    if (message === "reload") {
        setTimeout(() => location.reload(), 400);
        return;
    }
    if (message === "paused") {
        const el = document.getElementById("room-paused-banner");
        if (el) el.hidden = false;
        return;
    }
    if (message === "resumed") {
        const el = document.getElementById("room-paused-banner");
        if (el) el.hidden = true;
        document.body.dispatchEvent(new Event("room-refresh"));
        return;
    }
    if (message.startsWith("status:")) {
        const el = document.getElementById("room-phone-status");
        if (el) el.textContent = message.slice(7);
    }
}
