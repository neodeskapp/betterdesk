package onboard

// The offer to keep NeoDesk on the computer after quick support (the
// receiver app). An admin turns it on or off in the console (Settings,
// Quick support; on until changed); while it is on, the redeem answer of
// a quick support session carries a one-time admission the API server
// makes (api/receiver_admission.go), with which the installed app enrolls
// in managed or locked mode without a join request. Wire format:
// routes/onboard/PROTOCOL.md.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// receiverOfferKey is the settings row of the switch: "0" off, anything
// else (or no row) on.
const receiverOfferKey = "quick_support_receiver_offer"

// LoadReceiverOffer reads whether quick support offers to keep the app;
// never saved, it does.
func LoadReceiverOffer(ctx context.Context, q store.Querier) (bool, error) {
	var raw sql.NullString
	err := q.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, receiverOfferKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the receiver offer: %w", err)
	}
	return !raw.Valid || raw.String != "0", nil
}

// SaveReceiverOffer stores the switch.
func SaveReceiverOffer(ctx context.Context, q store.Querier, on bool) error {
	value := "0"
	if on {
		value = "1"
	}
	now := store.NowSQL(q.Dialect())
	_, err := q.Exec(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, ?, "+now+") "+
		"ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = "+now, receiverOfferKey, value)
	if err != nil {
		return fmt.Errorf("store the receiver offer: %w", err)
	}
	return nil
}

// Admission is a one-time admission of a quick support session and when
// it stops working (RFC 3339).
type Admission struct {
	Secret    string
	ExpiresAt string
}

// Admission asks the API server for an admission of the session.
func (c *Codes) Admission(ctx context.Context, sessionID string) (Admission, error) {
	resp, err := c.api.Request(ctx, http.MethodPost, "/onboard/admissions",
		goapi.Body(map[string]string{"session": sessionID}), goapi.ValidateStatus(func(int) bool { return true }))
	if err != nil {
		return Admission{}, fmt.Errorf("make an admission: %w", err)
	}
	var out struct {
		Success   bool   `json:"success"`
		Admission string `json:"admission"`
		ExpiresAt string `json:"expires_at"`
		Error     string `json:"error"`
	}
	if err := resp.Decode(&out); err != nil {
		return Admission{}, fmt.Errorf("make an admission: answer %d is no JSON: %w", resp.Status, err)
	}
	if resp.Status != http.StatusOK || !out.Success || out.Admission == "" {
		return Admission{}, fmt.Errorf("make an admission: the API server answered %d %s", resp.Status, out.Error)
	}
	return Admission{Secret: out.Admission, ExpiresAt: out.ExpiresAt}, nil
}
