package mail

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
)

// The notifications a reader can turn off from the mail itself: each
// recurring notification mail carries List-Unsubscribe with a link that
// works without signing in (RFC 8058 one-click), and the footer names it.
const (
	NotifyChat  = "chat"  // someone waits in the chat
	NotifyAlert = "alert" // an alert rule fired
	NotifyHelp  = "help"  // a device asked for help
)

// notifyKinds are the kinds a link may name.
var notifyKinds = map[string]bool{NotifyChat: true, NotifyAlert: true, NotifyHelp: true}

// The settings rows: the key the links are signed with, made on first
// use, and the addresses that turned a kind off.
const (
	unsubscribeKeyRow = "mail_unsubscribe_key"
	unsubscribedRow   = "mail_unsubscribed"
)

// UnsubscribePath is the route of the links, below the console's address.
const UnsubscribePath = "/mail/unsubscribe"

var unsubscribeMu sync.Mutex

// unsubscribeKey is the signing key, made and stored when there is none.
func (s *Service) unsubscribeKey(ctx context.Context) ([]byte, error) {
	if s.opts.DB == nil {
		return nil, errors.New("no database")
	}
	unsubscribeMu.Lock()
	defer unsubscribeMu.Unlock()
	raw, ok, err := getSetting(ctx, s.opts.DB, unsubscribeKeyRow)
	if err != nil {
		return nil, err
	}
	if key, err := hex.DecodeString(raw); ok && err == nil && len(key) == 32 {
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := setSetting(ctx, s.opts.DB, unsubscribeKeyRow, hex.EncodeToString(key)); err != nil {
		return nil, err
	}
	return key, nil
}

func normalAddress(addr string) string { return strings.ToLower(strings.TrimSpace(addr)) }

func sign(key []byte, payload string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// UnsubscribeToken is the token of a link that turns the kind off for
// the address: the kind and the address, signed.
func (s *Service) UnsubscribeToken(ctx context.Context, kind, addr string) (string, error) {
	if !notifyKinds[kind] {
		return "", errors.New("unknown notification")
	}
	key, err := s.unsubscribeKey(ctx)
	if err != nil {
		return "", err
	}
	payload := kind + ":" + normalAddress(addr)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sign(key, payload), nil
}

// UnsubscribeURL is the link for the address, "" without the console's
// public address or a database.
func (s *Service) UnsubscribeURL(ctx context.Context, kind, addr string) string {
	base := s.GetPanelBaseURL()
	if base == "" {
		return ""
	}
	token, err := s.UnsubscribeToken(ctx, kind, addr)
	if err != nil {
		return ""
	}
	return base + UnsubscribePath + "?t=" + token
}

// ReadUnsubscribeToken checks a token and says what it turns off.
func (s *Service) ReadUnsubscribeToken(ctx context.Context, token string) (kind, addr string, ok bool) {
	body, mac, found := strings.Cut(token, ".")
	if !found {
		return "", "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", "", false
	}
	key, err := s.unsubscribeKey(ctx)
	if err != nil || !hmac.Equal([]byte(sign(key, string(payload))), []byte(mac)) {
		return "", "", false
	}
	kind, addr, found = strings.Cut(string(payload), ":")
	if !found || !notifyKinds[kind] || addr == "" {
		return "", "", false
	}
	return kind, addr, true
}

// unsubscribed reads the addresses that turned each kind off.
func (s *Service) unsubscribed(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	if s.opts.DB == nil {
		return out
	}
	raw, ok, err := getSetting(ctx, s.opts.DB, unsubscribedRow)
	if err != nil || !ok {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// Unsubscribed reports whether the address turned the kind off.
func (s *Service) Unsubscribed(ctx context.Context, kind, addr string) bool {
	return slices.Contains(s.unsubscribed(ctx)[kind], normalAddress(addr))
}

// SetUnsubscribed turns the kind off (off true) or on again for the
// address.
func (s *Service) SetUnsubscribed(ctx context.Context, kind, addr string, off bool) error {
	if !notifyKinds[kind] || s.opts.DB == nil {
		return errors.New("unknown notification")
	}
	unsubscribeMu.Lock()
	defer unsubscribeMu.Unlock()
	all := s.unsubscribed(ctx)
	addr = normalAddress(addr)
	list := slices.DeleteFunc(all[kind], func(a string) bool { return a == addr })
	if off {
		list = append(list, addr)
		slices.Sort(list)
	}
	all[kind] = list
	text, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return setSetting(ctx, s.opts.DB, unsubscribedRow, string(text))
}

// UnsubscribedList is every address that turned a kind off, by kind.
func (s *Service) UnsubscribedList(ctx context.Context) map[string][]string {
	all := s.unsubscribed(ctx)
	for kind := range notifyKinds {
		if all[kind] == nil {
			all[kind] = []string{}
		}
	}
	return all
}
