// Package onboard holds what "Add a computer" and "Quick support" share
// below the routes: the join and support codes (kept by the API server,
// reached in process), the links and file names made from a code, the
// quick support sessions with the session passwords, and the invite
// mails.
//
// A code is 8 symbols of the pairing alphabet. People see it in two
// groups of four (ABCD-EF23); links and file names carry it without the
// dash. Every form is read back with case and separators ignored.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// The kinds of code.
const (
	KindJoin    = "join"
	KindSupport = "support"
)

// CodeLength is the number of symbols of a code.
const CodeLength = 8

// alphabet is the pairing alphabet: no 0, 1, I, L, O or U.
const alphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// ErrUnusable is a code that is unknown, expired, used up, revoked or of
// another kind. Nothing tells these apart.
var ErrUnusable = errors.New("invalid_or_expired")

// ErrNotFound is a code id that names no open code.
var ErrNotFound = errors.New("not_found")

// ErrTooManyCodes is the answer when 500 codes are open.
var ErrTooManyCodes = errors.New("too_many_codes")

// RefusedError is a device the enrollment rules keep out (banned,
// removed, rejected, another machine under the same id). For a join under a
// known ID refused for its key's proof, Unproved names why (no_proof,
// bad_proof, replayed, empty_key, empty_uuid), and Audit says the console
// writes the audit entry now, standing for Repeats refusals since the last.
type RefusedError struct {
	Reason   string
	Unproved string
	Audit    bool
	Repeats  int
}

func (e *RefusedError) Error() string { return "device refused: " + e.Reason }

// Code is a join or support code as the API server keeps it, without the
// code itself.
type Code struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	FolderID    int64  `json:"folder_id"`
	Email       string `json:"email"`
	CreatedBy   string `json:"created_by"`
	CreatedByID int64  `json:"created_by_id"`
	// CreatedByToken is the console API token the code was made with, ""
	// for none: the end of that token ends the code and its session.
	CreatedByToken string `json:"created_by_token,omitempty"`
	CreatedAt      string `json:"created_at"`
	ExpiresAt      string `json:"expires_at"`
	MaxUses        int    `json:"max_uses"`
	UsesLeft       int    `json:"uses_left"`
}

// NewCode is what a code is made with.
type NewCode struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	FolderID    int64  `json:"folder_id"`
	Email       string `json:"email"`
	TTLSeconds  int64  `json:"ttl_seconds"`
	MaxUses     int    `json:"max_uses"`
	CreatedByID int64  `json:"created_by_id"`
}

// Device is a device to admit: what its client reported and the name the
// console gives it.
type Device struct {
	DeviceID    string `json:"device_id"`
	UUID        string `json:"uuid,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Platform    string `json:"platform,omitempty"`
	Version     string `json:"version,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Temporary   bool   `json:"temporary,omitempty"`
	// Proof is the key proof the client sent with its join, nil without
	// one. The API server checks it for a device ID it knows.
	Proof *Proof `json:"proof,omitempty"`
}

// Proof is a client's key proof of a join: the headers
// X-ND-Enrollment-Timestamp, -Nonce and -Signature, and the path the
// client signed (the request's path as it sent it).
type Proof struct {
	Path      string `json:"path"`
	Timestamp string `json:"timestamp"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

// Codes reaches the API server's routes of join and support codes.
type Codes struct {
	api *goapi.Client
}

// NewCodes returns the codes of the API server behind api.
func NewCodes(api *goapi.Client) *Codes { return &Codes{api: api} }

// answer is the API server's answer to a code route.
type answer struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Record  Code   `json:"record"`
	Codes   []Code `json:"codes"`
	Created bool   `json:"created"`
	Removed bool   `json:"removed"`
	Device  struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	} `json:"device"`
	Joined []Joined `json:"joined"`
	// Unproved, Audit and Repeats come with a join refused for its proof
	Unproved string   `json:"unproved"`
	Audit    bool     `json:"audit"`
	Repeats  int      `json:"repeats"`
	Refused  *Refused `json:"refused"`
}

// Joined is a computer that joined with an invite, and when.
type Joined struct {
	DeviceID string `json:"device_id"`
	JoinedAt string `json:"joined_at"`
}

// Refused is the last computer an invite's join refused: Reason is
// key_changed (an enrolled ID came with another key, which an
// administrator can approve) or refused.
type Refused struct {
	DeviceID string `json:"device_id"`
	Reason   string `json:"reason"`
	At       string `json:"at"`
}

// InviteState is what happened with an invite: its record, the computers
// that joined with it and the last one it refused (nil for none).
type InviteState struct {
	Code    Code
	Joined  []Joined
	Refused *Refused
}

// Invite returns what happened with the invite (join code) of the id, also
// once its joins used it up, until it would have expired. ErrNotFound for
// any other id.
func (c *Codes) Invite(ctx context.Context, id string) (InviteState, error) {
	status, out, err := c.call(ctx, http.MethodGet, "/onboard/codes/"+id, nil)
	if err != nil {
		return InviteState{}, err
	}
	if status != http.StatusOK || !out.Success {
		return InviteState{}, failure("read invite", status, out)
	}
	if out.Joined == nil {
		out.Joined = []Joined{}
	}
	return InviteState{Code: out.Record, Joined: out.Joined, Refused: out.Refused}, nil
}

// call sends one request and reads the answer whatever its status.
func (c *Codes) call(ctx context.Context, method, path string, body any) (int, answer, error) {
	opts := []goapi.CallOption{goapi.ValidateStatus(func(int) bool { return true })}
	if body != nil {
		opts = append(opts, goapi.Body(body))
	}
	resp, err := c.api.Request(ctx, method, path, opts...)
	if err != nil {
		return 0, answer{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	var out answer
	if err := resp.Decode(&out); err != nil {
		return resp.Status, answer{}, fmt.Errorf("%s %s: answer %d is no JSON: %w", method, path, resp.Status, err)
	}
	return resp.Status, out, nil
}

// failure turns an answer that is not a success into an error.
func failure(what string, status int, out answer) error {
	switch {
	case status == http.StatusNotFound && out.Error == ErrUnusable.Error():
		return ErrUnusable
	case status == http.StatusNotFound && out.Error == ErrNotFound.Error():
		return ErrNotFound
	case status == http.StatusConflict && out.Error == ErrTooManyCodes.Error():
		return ErrTooManyCodes
	case status == http.StatusForbidden && out.Error == "device_refused":
		return &RefusedError{Reason: out.Message, Unproved: out.Unproved, Audit: out.Audit, Repeats: out.Repeats}
	}
	return fmt.Errorf("%s: the API server answered %d %s %s", what, status, out.Error, out.Message)
}

// Create makes a code and returns its text, which nothing keeps.
func (c *Codes) Create(ctx context.Context, n NewCode) (string, Code, error) {
	status, out, err := c.call(ctx, http.MethodPost, "/onboard/codes", n)
	if err != nil {
		return "", Code{}, err
	}
	if status != http.StatusCreated || !out.Success {
		return "", Code{}, failure("create code", status, out)
	}
	return out.Code, out.Record, nil
}

// List returns the open codes of a kind, newest first.
func (c *Codes) List(ctx context.Context, kind string) ([]Code, error) {
	status, out, err := c.call(ctx, http.MethodGet, "/onboard/codes?kind="+kind, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || !out.Success {
		return nil, failure("list codes", status, out)
	}
	if out.Codes == nil {
		out.Codes = []Code{}
	}
	return out.Codes, nil
}

// Revoke deletes an open code and returns what it was.
func (c *Codes) Revoke(ctx context.Context, id string) (Code, error) {
	status, out, err := c.call(ctx, http.MethodDelete, "/onboard/codes/"+id, nil)
	if err != nil {
		return Code{}, err
	}
	if status != http.StatusOK || !out.Success {
		return Code{}, failure("revoke code", status, out)
	}
	return out.Record, nil
}

// Lookup returns the record of a usable code without spending it.
func (c *Codes) Lookup(ctx context.Context, code string) (Code, error) {
	status, out, err := c.call(ctx, http.MethodPost, "/onboard/codes/lookup", map[string]string{"code": code})
	if err != nil {
		return Code{}, err
	}
	if status != http.StatusOK || !out.Success {
		return Code{}, failure("look up code", status, out)
	}
	return out.Record, nil
}

// Redeemed is the device a join redeem admitted.
type Redeemed struct {
	DeviceID string
	Created  bool
}

// Redeem spends one use of a code of the kind. With a device, the device
// is admitted first; a device the rules keep out is a *RefusedError and
// spends nothing.
func (c *Codes) Redeem(ctx context.Context, code, kind string, dev *Device) (Code, Redeemed, error) {
	body := map[string]any{"code": code, "kind": kind}
	if dev != nil {
		body["device"] = dev
	}
	status, out, err := c.call(ctx, http.MethodPost, "/onboard/codes/redeem", body)
	if err != nil {
		return Code{}, Redeemed{}, err
	}
	if status == http.StatusBadRequest && out.Error == "invalid_device" {
		return Code{}, Redeemed{}, &InvalidDeviceError{Reason: out.Message}
	}
	if status != http.StatusOK || !out.Success {
		return Code{}, Redeemed{}, failure("redeem code", status, out)
	}
	return out.Record, Redeemed{DeviceID: out.Device.ID, Created: out.Device.Created}, nil
}

// InvalidDeviceError is a device id or field the API server does not take.
type InvalidDeviceError struct{ Reason string }

func (e *InvalidDeviceError) Error() string { return "invalid device: " + e.Reason }

// Admit makes a device approved without a code (a quick support session's
// device) and reports whether it is new.
func (c *Codes) Admit(ctx context.Context, dev Device) (bool, error) {
	status, out, err := c.call(ctx, http.MethodPost, "/onboard/devices", dev)
	if err != nil {
		return false, err
	}
	if status == http.StatusBadRequest && out.Error == "invalid_device" {
		return false, &InvalidDeviceError{Reason: out.Message}
	}
	if status != http.StatusOK || !out.Success {
		return false, failure("admit device", status, out)
	}
	return out.Created, nil
}

// RemoveDevice deletes a temporary quick support device; any other device
// stays and the answer is false.
func (c *Codes) RemoveDevice(ctx context.Context, id string) (bool, error) {
	status, out, err := c.call(ctx, http.MethodDelete, "/onboard/devices/"+id, nil)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK || !out.Success {
		return false, failure("remove device", status, out)
	}
	return out.Removed, nil
}

// Prune deletes expired and used codes.
func (c *Codes) Prune(ctx context.Context) error {
	status, out, err := c.call(ctx, http.MethodPost, "/onboard/codes/prune", map[string]any{})
	if err != nil {
		return err
	}
	if status != http.StatusOK || !out.Success {
		return failure("prune codes", status, out)
	}
	return nil
}

// Normalize returns a code without separators and in upper case.
func Normalize(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch r {
		case ' ', '-', '\t':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ValidFormat reports whether code (separators and case ignored) has the
// form of a join or support code.
func ValidFormat(code string) bool {
	n := Normalize(code)
	if len(n) != CodeLength {
		return false
	}
	for _, r := range n {
		if !strings.ContainsRune(alphabet, r) {
			return false
		}
	}
	return true
}

// Display returns a code as people read it: ABCD-EF23.
func Display(code string) string {
	n := Normalize(code)
	if len(n) != CodeLength {
		return n
	}
	return n[:4] + "-" + n[4:]
}
