package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Subscribers receive invalidations, never account data. Channels coalesce bursts.
type syncSubscriber struct {
	userID  string
	changed chan struct{}
}

func (s *store) subscribe(userID string) (*syncSubscriber, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscribers == nil {
		s.subscribers = make(map[*syncSubscriber]bool)
	}
	count := 0
	for existing := range s.subscribers {
		if existing.userID == userID {
			count++
		}
	}
	if count >= 16 {
		return nil, func() {}
	}
	sub := &syncSubscriber{userID, make(chan struct{}, 1)}
	s.subscribers[sub] = true
	return sub, func() { s.mu.Lock(); delete(s.subscribers, sub); s.mu.Unlock() }
}
func syncState(d database, id string) any {
	devices := map[string]*device{}
	deliveries := map[string]*delivery{}
	for k, v := range d.Devices {
		if v.UserID == id {
			devices[k] = v
		}
	}
	for k, v := range d.Deliveries {
		if v.UserID == id || v.SenderUserID == id {
			deliveries[k] = v
		}
	}
	return []any{d.Users[id], d.Messages[id], devices, deliveries, d.Topics}
}

// Called under the store lock only after a successful commit.
func (s *store) notifyChanges(before, after database) {
	for id, d := range after.Deliveries {
		old := before.Deliveries[id]
		if old == nil || old.Status != d.Status || old.Attempts != d.Attempts {
			previous := "new"
			if old != nil {
				previous = old.Status
			}
			slog.Info("push.state", "delivery_id", id, "message_id", d.MessageID, "device_id", d.DeviceID, "user_id", d.UserID, "from", previous, "to", d.Status, "attempts", d.Attempts, "next_attempt_at", d.NextAttemptAt)
		}
	}

	changed := map[string]bool{}
	checked := map[string]bool{}
	for sub := range s.subscribers {
		id := sub.userID
		if !checked[id] {
			changed[id] = !reflect.DeepEqual(syncState(before, id), syncState(after, id))
			checked[id] = true
		}
		if changed[id] {
			select {
			case sub.changed <- struct{}{}:
			default:
			}
		}
	}
}

// The sync socket is server-to-client only. No writes to account state are
// accepted over it; existing authenticated HTTP endpoints remain authoritative.
func (a *application) syncSocket(w http.ResponseWriter, r *http.Request, u *user) {
	if r.Header.Get("Origin") != a.cfg.origin {
		failure(w, 403, "Invalid origin")
		return
	}
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	if err != nil || len(key) != 16 || r.Header.Get("Sec-WebSocket-Version") != "13" || !headerToken(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		failure(w, 400, "Invalid WebSocket handshake")
		return
	}
	sub, unsubscribe := a.db.subscribe(u.ID)
	defer unsubscribe()
	if sub == nil {
		failure(w, 429, "Too many live connections")
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		failure(w, 500, "WebSocket unavailable")
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	digest := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(digest[:]) + "\r\n\r\n"); err != nil {
		return
	}
	if rw.Flush() != nil {
		return
	}
	slog.Info("websocket.connected", requestFields(r)...)
	defer func() { slog.Info("websocket.disconnected", requestFields(r)...) }()
	socket := &syncWire{conn: conn}
	conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	done := make(chan struct{})
	go func() { defer close(done); defer conn.Close(); socket.read(rw.Reader) }()
	token := a.sessionToken(r)
	valid := func() bool { return a.auth.lookupSession(token) == u.ID && a.db.userByID(u.ID) != nil }
	if !valid() {
		return
	}
	if socket.write(1, []byte(`{"type":"sync"}`)) != nil {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case <-sub.changed:
			if !valid() {
				return
			}
			if socket.write(1, []byte(`{"type":"sync"}`)) != nil {
				return
			}
		case <-ticker.C:
			if !valid() {
				return
			}
			if socket.write(9, nil) != nil {
				return
			}
		}
	}
}
func headerToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

type syncWire struct {
	conn net.Conn
	mu   sync.Mutex
}

func (s *syncWire) write(op byte, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(payload) > 125 {
		return errors.New("sync frame too large")
	}
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	frame := append([]byte{0x80 | op, byte(len(payload))}, payload...)
	for len(frame) > 0 {
		n, err := s.conn.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
func (s *syncWire) read(reader *bufio.Reader) {
	for {
		var header [2]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return
		}
		// Only masked, unfragmented control frames are accepted from the client.
		op := header[0] & 15
		n := int(header[1] & 127)
		if header[0]&0xf0 != 0x80 || header[1]&0x80 == 0 || n > 125 || (op != 8 && op != 9 && op != 10) {
			return
		}
		var mask [4]byte
		if _, err := io.ReadFull(reader, mask[:]); err != nil {
			return
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		switch op {
		case 8:
			// A close payload cannot contain a truncated status code.
			if n == 1 {
				return
			}
			if n >= 2 {
				code := binary.BigEndian.Uint16(payload)
				if code < 1000 {
					return
				}
			}
			s.write(8, nil)
			return
		case 9:
			if s.write(10, payload) != nil {
				return
			}
		case 10:
			s.conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		}
	}
}
