package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/go-webauthn/webauthn/webauthn"
)

type user struct {
	APIKeys     []personalAPIKey      `json:"apiKeys,omitempty"`
	Theme       string                `json:"theme,omitempty"`
	Language    string                `json:"language,omitempty"`
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Role        string                `json:"role,omitempty"`
	Credentials []webauthn.Credential `json:"credentials"`
	Topics      []string              `json:"topics"`
	CreatedAt   time.Time             `json:"createdAt"`
}

func (u *user) WebAuthnID() []byte                         { b, _ := base64.RawURLEncoding.DecodeString(u.ID); return b }
func (u *user) WebAuthnName() string                       { return u.Name }
func (u *user) WebAuthnDisplayName() string                { return u.Name }
func (u *user) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

func (u *user) isAdmin() bool { return u != nil && u.Role == "admin" }

type inboxMessage struct {
	SentDeleted  bool       `json:"sentDeleted,omitempty"` // Hide sender history without removing recipient data or delivery jobs.
	SenderUserID string     `json:"senderUserId,omitempty"`
	LongText     string     `json:"longText,omitempty"`
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	Topic        string     `json:"topic,omitempty"`
	Tag          string     `json:"tag,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	ReadAt       *time.Time `json:"readAt,omitempty"`
}

type device struct {
	ID           string               `json:"id"`
	UserID       string               `json:"userId"`
	Name         string               `json:"name"`
	Subscription webpush.Subscription `json:"subscription"`
	CreatedAt    time.Time            `json:"createdAt"`
}

type topic struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type invitation struct {
	Name    string    `json:"name"`
	Expires time.Time `json:"expires"`
}

type idempotencyRecord struct {
	Result      sendResult `json:"result"`
	PayloadHash string     `json:"payloadHash"`
	CreatedAt   time.Time  `json:"createdAt"`
}

type database struct {
	MessageLogDisabled bool                         `json:"messageLogDisabled,omitempty"`
	MessageLog         map[string]*messageLogRecord `json:"messageLog,omitempty"`
	IntegrationKeys    []personalAPIKey             `json:"integrationKeys,omitempty"`
	Users              map[string]*user             `json:"users"`
	Devices            map[string]*device           `json:"devices"`
	Topics             map[string]*topic            `json:"topics"`
	Invites            map[string]*invitation       `json:"invites"` // SHA-256 of bearer invitation, never plaintext
	Messages           map[string][]inboxMessage    `json:"messages"`
	Deliveries         map[string]*delivery         `json:"deliveries,omitempty"`
	Idempotency        map[string]idempotencyRecord `json:"idempotency,omitempty"`
	BootstrapUsed      bool                         `json:"bootstrapUsed"`
}

type store struct {
	subscribers map[*syncSubscriber]bool
	mu          sync.RWMutex
	path        string
	db          database
	sqlDB       *sql.DB
}

func (d *database) initialize() {
	if d.MessageLog == nil {
		d.MessageLog = make(map[string]*messageLogRecord)
	}
	if d.Users == nil {
		d.Users = make(map[string]*user)
	}
	if d.Devices == nil {
		d.Devices = make(map[string]*device)
	}
	if d.Topics == nil {
		d.Topics = make(map[string]*topic)
	}
	if d.Invites == nil {
		d.Invites = make(map[string]*invitation)
	}
	if d.Messages == nil {
		d.Messages = make(map[string][]inboxMessage)
	}
	if d.Deliveries == nil {
		d.Deliveries = make(map[string]*delivery)
	}
	if d.Idempotency == nil {
		d.Idempotency = make(map[string]idempotencyRecord)
	}
}

func (s *store) userByID(id string) *user {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u := s.db.Users[id]; u != nil {
		return cloneUser(u)
	}
	return nil
}
func cloneUser(u *user) *user {
	b, _ := json.Marshal(u)
	var out user
	_ = json.Unmarshal(b, &out)
	return &out
}
func (s *store) findPasskey(rawID, handle []byte) (*user, error) {
	id := base64.RawURLEncoding.EncodeToString(handle)
	u := s.userByID(id)
	if u == nil {
		return nil, errors.New("unknown user")
	}
	for _, c := range u.Credentials {
		if constantBytesEqual(c.ID, rawID) {
			return u, nil
		}
	}
	return nil, errors.New("unknown passkey")
}
func (s *store) snapshot() database {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Never return mutable store maps or pointers to HTTP handlers.
	b, _ := json.Marshal(s.db)
	var out database
	_ = json.Unmarshal(b, &out)
	out.initialize()
	return out
}
func (s *store) putCredential(userID string, credential webauthn.Credential) error {
	return s.update(func(d *database) error {
		u := d.Users[userID]
		if u == nil {
			return errors.New("user deleted")
		}
		for i := range u.Credentials {
			if constantBytesEqual(u.Credentials[i].ID, credential.ID) {
				u.Credentials[i] = credential
				return nil
			}
		}
		return errors.New("credential not found")
	})
}
func (s *store) devicesFor(topicName, userID, deviceID string) []device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []device{}
	for _, dev := range s.db.Devices {
		u := s.db.Users[dev.UserID]
		if u == nil {
			continue
		}
		if deviceID != "" && dev.ID != deviceID {
			continue
		}
		if userID != "" && dev.UserID != userID {
			continue
		}
		if topicName != "" && !hasString(u.Topics, topicName) {
			continue
		}
		result = append(result, *dev)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (s *store) pruneDevices(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.update(func(d *database) error {
		for _, id := range ids {
			delete(d.Devices, id)
		}
		return nil
	})
}
func invitationHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func hasString(a []string, v string) bool {
	for _, s := range a {
		if s == v {
			return true
		}
	}
	return false
}

func (s *store) bootstrapAvailable(code string) bool {
	if code == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.db.BootstrapUsed && len(s.db.Users) == 0
}

// registerBootstrapAdmin consumes first-run setup atomically with storing the new admin.
// Passkey validation must already have succeeded before calling this function.
func (s *store) registerBootstrapAdmin(account *user) error {
	if account == nil || len(account.Credentials) == 0 {
		return errors.New("passkey required")
	}
	return s.update(func(d *database) error {
		if d.BootstrapUsed || len(d.Users) != 0 {
			return errors.New("bootstrap already consumed")
		}
		if d.Users[account.ID] != nil {
			return errors.New("duplicate user")
		}
		admin := cloneUser(account)
		admin.Role = "admin"
		d.Users[admin.ID] = admin
		d.BootstrapUsed = true
		return nil
	})
}

// Read only the requesting account's data; unrelated messages, credentials,
// endpoints, and receipt secrets are never copied into user handlers.
func (s *store) userSnapshot(userID string, inbox bool) database {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out database
	out.initialize()
	if inbox {
		out.Messages[userID] = s.db.Messages[userID]
		for id, d := range s.db.Deliveries {
			if d.UserID == userID {
				copy := *d
				copy.ReceiptSecret = ""
				out.Deliveries[id] = &copy
			}
		}
	} else {
		for name, t := range s.db.Topics {
			out.Topics[name] = t
		}
		for id, d := range s.db.Devices {
			if d.UserID == userID {
				copy := *d
				copy.Subscription = webpush.Subscription{}
				out.Devices[id] = &copy
			}
		}
	}
	b, _ := json.Marshal(out)
	var copy database
	_ = json.Unmarshal(b, &copy)
	copy.initialize()
	return copy
}

func (s *store) invitationByHash(hash string) *invitation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if invite := s.db.Invites[hash]; invite != nil {
		copy := *invite
		return &copy
	}
	return nil
}
