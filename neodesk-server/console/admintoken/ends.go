package admintoken

// What ends with a token. A token can make credentials that outlive the
// request that made them: guest links, device tokens, quick support
// sessions with their codes, invites (join codes), pairing codes, mesh
// share links and organization invitations. Each one records the token's
// public id (created_by_token: a column of device_tokens and
// org_invitations, a field of the JSON the others keep in server_config).
// When the token ends, they end with it, in the same transaction:
//
//   - revoked: Revoke ends them as it revokes;
//   - expired: Sweep ends them soon after the expiry (Run wakes at the
//     next expiry, and at least once a minute);
//   - deleted (its row gone, as after a restore): Sweep ends what names a
//     token that is no longer there.
//
// What the same account made without the token stays. What runs on the
// credentials outside the database (a guest's session, a quick support
// session the console keeps in memory) is ended right after the commit
// by the functions OnEnd registered. Device tokens the server issues to a
// device it admitted (created_by "system") are the device's, not the
// token holder's: they stay, as do the devices a token approved.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Made counts credentials a token made, by kind: those still open, or
// those its end ended.
type Made struct {
	GuestLinks              int `json:"guest_links"`
	DeviceTokens            int `json:"device_tokens"`
	SupportSessions         int `json:"support_sessions"`
	Invites                 int `json:"invites"`
	PairingCodes            int `json:"pairing_codes"`
	ShareLinks              int `json:"share_links"`
	OrganizationInvitations int `json:"organization_invitations"`
}

// Total is how many there are of every kind.
func (m Made) Total() int {
	return m.GuestLinks + m.DeviceTokens + m.SupportSessions + m.Invites + m.PairingCodes + m.ShareLinks + m.OrganizationInvitations
}

// Text lists the counts that are not zero for the audit log: "1 guest
// link, 2 device tokens".
func (m Made) Text() string {
	var parts []string
	add := func(n int, one, many string) {
		switch {
		case n == 1:
			parts = append(parts, "1 "+one)
		case n > 1:
			parts = append(parts, strconv.Itoa(n)+" "+many)
		}
	}
	add(m.GuestLinks, "guest link", "guest links")
	add(m.DeviceTokens, "device token", "device tokens")
	add(m.SupportSessions, "support session", "support sessions")
	add(m.Invites, "invite", "invites")
	add(m.PairingCodes, "pairing code", "pairing codes")
	add(m.ShareLinks, "guest desktop link", "guest desktop links")
	add(m.OrganizationInvitations, "organization invitation", "organization invitations")
	return strings.Join(parts, ", ")
}

// Why a token ended.
const (
	EndRevoked = "revoked"
	EndExpired = "expired"
	EndDeleted = "deleted"
)

// Ending is what the end of one token ended.
type Ending struct {
	// TokenID is the token's public id.
	TokenID string
	// Token is the token's row as it is after the end, nil for a token
	// whose row is gone.
	Token *Token
	// Why is EndRevoked, EndExpired or EndDeleted.
	Why string
	// Ended counts what ended, by kind.
	Ended Made
	// GuestLinks are the ids of the guest links that ended, whose
	// running sessions end too.
	GuestLinks []string
	// SupportSessions are the ids of the quick support sessions that
	// ended.
	SupportSessions []string
}

// AuditAction is the audit log's action for the ending.
func (e *Ending) AuditAction() string { return "api_token_" + e.Why }

// AuditText is the audit log's entry for the ending: the token, what
// happened to it, and what ended with it.
func (e *Ending) AuditText() string {
	var head string
	switch {
	case e.Token == nil:
		head = "API token " + Prefix + e.TokenID + "_… was deleted"
	case e.Why == EndRevoked:
		head = "API token " + e.Token.Name + " (" + e.Token.Hint() + ") of " + e.Token.Username + " revoked"
	default:
		head = "API token " + e.Token.Name + " (" + e.Token.Hint() + ") of " + e.Token.Username + " " + e.Why
	}
	if e.Ended.Total() == 0 {
		return head + ". Nothing made with it was still open"
	}
	return head + ". Ended with it: " + e.Ended.Text()
}

// OnEnd adds a function that is called after a token's end is committed,
// with what ended: it ends what runs on those credentials outside the
// database. It must not fail the end; it logs what goes wrong.
func (s *Service) OnEnd(fn func(ctx context.Context, e *Ending)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onEnd = append(s.onEnd, fn)
}

func (s *Service) ended(ctx context.Context, e *Ending) {
	s.mu.Lock()
	listeners := make([]func(context.Context, *Ending), len(s.onEnd))
	copy(listeners, s.onEnd)
	s.mu.Unlock()
	for _, fn := range listeners {
		fn(ctx, e)
	}
}

// Revoke ends a token for good, and with it what it made that is still
// open, in one transaction. It answers nil when the token was not there
// or was revoked already.
func (s *Service) Revoke(ctx context.Context, id, by string) (*Ending, error) {
	now := s.now()
	var ending *Ending
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		res, err := tx.Exec(ctx, `UPDATE admin_tokens SET revoked_at = ?, revoked_by = ?, ended_at = ? WHERE public_id = ? AND revoked_at IS NULL`,
			now.UnixMilli(), by, now.UnixMilli(), id)
		if err != nil {
			return fmt.Errorf("revoke token: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		found, err := s.endMade(ctx, tx, map[string]bool{id: true}, now)
		if err != nil {
			return err
		}
		ending = found[id]
		if ending == nil {
			ending = &Ending{TokenID: id}
		}
		ending.Why = EndRevoked
		ending.Token, err = scanToken(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE t.public_id = ?`, id), nil)
		return err
	})
	if err != nil || ending == nil {
		return nil, err
	}
	s.ended(context.WithoutCancel(ctx), ending)
	return ending, nil
}

// Sweep ends what tokens made that ended without a revoke: a token past
// its expiry (once), and credentials that name a token whose row is gone.
// Each token's end is its own transaction. It returns the endings.
func (s *Service) Sweep(ctx context.Context) ([]*Ending, error) {
	now := s.now()
	rows, err := s.db.Query(ctx, `SELECT public_id FROM admin_tokens WHERE ended_at IS NULL AND (revoked_at IS NOT NULL OR expires_at <= ?)`, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("find ended tokens: %w", err)
	}
	var due []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		due = append(due, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []*Ending
	var errs []error
	for _, id := range due {
		var ending *Ending
		err := s.db.InTx(ctx, func(tx *store.Tx) error {
			res, err := tx.Exec(ctx, `UPDATE admin_tokens SET ended_at = ? WHERE public_id = ? AND ended_at IS NULL`, now.UnixMilli(), id)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n == 0 {
				return err
			}
			found, err := s.endMade(ctx, tx, map[string]bool{id: true}, now)
			if err != nil {
				return err
			}
			ending = found[id]
			if ending == nil {
				ending = &Ending{TokenID: id}
			}
			ending.Token, err = scanToken(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE t.public_id = ?`, id), nil)
			if err != nil {
				return err
			}
			ending.Why = EndExpired
			if !ending.Token.RevokedAt.IsZero() {
				ending.Why = EndRevoked
			}
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("end what token %s made: %w", id, err))
			continue
		}
		if ending != nil {
			s.ended(context.WithoutCancel(ctx), ending)
			out = append(out, ending)
		}
	}

	// Credentials that name a token that is gone
	var gone []*Ending
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		gone = nil
		named, err := s.openMade(ctx, tx, nil, now)
		if err != nil {
			return err
		}
		missing := map[string]bool{}
		for id := range named {
			var n int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM admin_tokens WHERE public_id = ?`, id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				missing[id] = true
			}
		}
		if len(missing) == 0 {
			return nil
		}
		found, err := s.endMade(ctx, tx, missing, now)
		if err != nil {
			return err
		}
		for _, id := range sortedKeys(found) {
			found[id].Why = EndDeleted
			gone = append(gone, found[id])
		}
		return nil
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("end what deleted tokens made: %w", err))
	}
	for _, e := range gone {
		s.ended(context.WithoutCancel(ctx), e)
		out = append(out, e)
	}
	return out, errors.Join(errs...)
}

// sweepEvery is the longest Run waits between two sweeps.
const sweepEvery = time.Minute

// Run sweeps until ctx ends: at the next expiry of a token, right after a
// token is made, and at least once a minute. report gets every ending and
// every error. While Options.Lease is false it waits without sweeping:
// another instance sweeps for the installation.
func (s *Service) Run(ctx context.Context, report func(e *Ending, err error)) {
	for {
		held := s.lease == nil || s.lease()
		if held {
			endings, err := s.Sweep(ctx)
			if ctx.Err() != nil {
				return
			}
			for _, e := range endings {
				report(e, nil)
			}
			if err != nil {
				report(nil, err)
			}
		}
		// An instance without the lease asks for it again in a minute
		wait := sweepEvery
		var next sql.NullInt64
		if held {
			if err := s.db.QueryRow(ctx, `SELECT MIN(expires_at) FROM admin_tokens WHERE ended_at IS NULL AND revoked_at IS NULL`).Scan(&next); err == nil && next.Valid {
				if until := time.UnixMilli(next.Int64).Sub(s.now()); until < wait {
					wait = until
				}
			}
		}
		// A token whose end failed is tried again in a second, not at once
		if wait < time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// OpenMade counts what the tokens ids made that is still open, for the
// tokens that have not ended: what their end would end.
func (s *Service) OpenMade(ctx context.Context, ids []string) (map[string]Made, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[string]Made{}
	if len(want) == 0 {
		return out, nil
	}
	found, err := s.openMade(ctx, s.db, want, s.now())
	if err != nil {
		return nil, err
	}
	for id, e := range found {
		out[id] = e.Ended
	}
	return out, nil
}

func sortedKeys(m map[string]*Ending) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The prefixes of the server_config rows that hold credentials.
const (
	guestLinkPrefix      = "gal_"
	pairingCodePrefix    = "pairing_code_"
	supportSessionPrefix = "support_session_"
	shareLinkPrefix      = "mesh_share_"
)

// madeRow is a server_config row that holds a credential.
type madeRow struct {
	key, value string
	fields     map[string]json.RawMessage
	token      string
}

func (r *madeRow) text(field string) string {
	var v string
	_ = json.Unmarshal(r.fields[field], &v)
	return v
}

func (r *madeRow) number(field string) int64 {
	var v float64
	_ = json.Unmarshal(r.fields[field], &v)
	return int64(v)
}

func (r *madeRow) time(field string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, r.text(field))
	return t, err == nil
}

// open reports whether the credential of the row still works at now.
func (r *madeRow) open(now time.Time) bool {
	switch {
	case strings.HasPrefix(r.key, guestLinkPrefix):
		if revoked, ok := r.fields["revoked_at"]; ok && string(revoked) != "null" {
			return false
		}
		if end, ok := r.time("expires_at"); !ok || !now.Before(end) {
			return false
		}
		max := r.number("max_uses")
		return max <= 0 || r.number("use_count") < max
	case strings.HasPrefix(r.key, pairingCodePrefix):
		end, ok := r.time("expires_at")
		return ok && now.Before(end) && r.number("uses_left") > 0
	case strings.HasPrefix(r.key, supportSessionPrefix):
		switch r.text("status") {
		case "waiting", "opened", "asking", "allowed":
			return true
		}
		return false
	case strings.HasPrefix(r.key, shareLinkPrefix):
		end, ok := r.time("expires_at")
		return ok && now.Before(end)
	}
	return false
}

// madeRows reads the server_config rows that hold a credential made with
// a token: those of the tokens in want, or of any token for want nil.
func madeRows(ctx context.Context, q store.Querier, want map[string]bool) ([]*madeRow, error) {
	if ok, err := hasTable(ctx, q, "server_config"); err != nil || !ok {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT key, value FROM server_config
		WHERE (key LIKE 'gal\_%' ESCAPE '\' OR key LIKE 'pairing\_code\_%' ESCAPE '\' OR key LIKE 'support\_session\_%' ESCAPE '\' OR key LIKE 'mesh\_share\_%' ESCAPE '\')
		AND value LIKE '%"created\_by\_token":"%' ESCAPE '\'`)
	if err != nil {
		return nil, fmt.Errorf("read what tokens made: %w", err)
	}
	defer rows.Close()
	var out []*madeRow
	for rows.Next() {
		r := &madeRow{}
		if err := rows.Scan(&r.key, &r.value); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(r.value), &r.fields) != nil {
			continue
		}
		r.token = r.text("created_by_token")
		if r.token == "" || (want != nil && !want[r.token]) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// openMade counts what the tokens in want (any token for nil) made that
// is still open, without changing anything.
func (s *Service) openMade(ctx context.Context, q store.Querier, want map[string]bool, now time.Time) (map[string]*Ending, error) {
	return s.walkMade(ctx, q, want, now, false)
}

// endMade ends what the tokens in want made that is still open, in the
// transaction q, and counts it.
func (s *Service) endMade(ctx context.Context, q store.Querier, want map[string]bool, now time.Time) (map[string]*Ending, error) {
	return s.walkMade(ctx, q, want, now, true)
}

func (s *Service) walkMade(ctx context.Context, q store.Querier, want map[string]bool, now time.Time, end bool) (map[string]*Ending, error) {
	out := map[string]*Ending{}
	of := func(id string) *Ending {
		if out[id] == nil {
			out[id] = &Ending{TokenID: id}
		}
		return out[id]
	}

	rows, err := madeRows(ctx, q, want)
	if err != nil {
		return nil, err
	}
	// A support code and its session have the same id: one session
	supportCodes := map[string]map[string]bool{}
	for _, r := range rows {
		if !r.open(now) {
			continue
		}
		e := of(r.token)
		switch {
		case strings.HasPrefix(r.key, guestLinkPrefix):
			if end {
				if err := endRow(ctx, q, r, func(f map[string]json.RawMessage) {
					f["revoked_at"], _ = json.Marshal(now.UTC())
				}); err != nil {
					return nil, err
				}
			}
			e.Ended.GuestLinks++
			e.GuestLinks = append(e.GuestLinks, r.text("id"))
		case strings.HasPrefix(r.key, pairingCodePrefix):
			if end {
				if _, err := q.Exec(ctx, `DELETE FROM server_config WHERE key = ?`, r.key); err != nil {
					return nil, fmt.Errorf("end a code: %w", err)
				}
			}
			switch r.text("kind") {
			case "join":
				e.Ended.Invites++
			case "support":
				if supportCodes[r.token] == nil {
					supportCodes[r.token] = map[string]bool{}
				}
				supportCodes[r.token][r.text("id")] = true
			default:
				e.Ended.PairingCodes++
			}
		case strings.HasPrefix(r.key, supportSessionPrefix):
			if end {
				stamp := now.UTC().Format(time.RFC3339)
				if err := endRow(ctx, q, r, func(f map[string]json.RawMessage) {
					status := "ended"
					var current string
					_ = json.Unmarshal(f["status"], &current)
					if current == "waiting" {
						status = "expired"
					}
					f["status"], _ = json.Marshal(status)
					f["ended_at"], _ = json.Marshal(stamp)
					f["ended_by"], _ = json.Marshal("token_ended")
				}); err != nil {
					return nil, err
				}
			}
			if supportCodes[r.token] == nil {
				supportCodes[r.token] = map[string]bool{}
			}
			supportCodes[r.token][r.text("id")] = true
		case strings.HasPrefix(r.key, shareLinkPrefix):
			if end {
				if _, err := q.Exec(ctx, `DELETE FROM server_config WHERE key = ?`, r.key); err != nil {
					return nil, fmt.Errorf("end a share link: %w", err)
				}
			}
			e.Ended.ShareLinks++
		}
	}
	for token, ids := range supportCodes {
		e := of(token)
		e.Ended.SupportSessions += len(ids)
		for id := range ids {
			e.SupportSessions = append(e.SupportSessions, id)
		}
		sort.Strings(e.SupportSessions)
	}

	if err := walkTable(ctx, q, want, now, end, "device_tokens", of); err != nil {
		return nil, err
	}
	if err := walkTable(ctx, q, want, now, end, "org_invitations", of); err != nil {
		return nil, err
	}
	for id, e := range out {
		if e.Ended.Total() == 0 && end {
			continue
		}
		if e.Ended.Total() == 0 {
			delete(out, id)
		}
	}
	return out, nil
}

// endRow changes the JSON of a row, as long as nobody changed the row
// since it was read; a row changed meanwhile is read again.
func endRow(ctx context.Context, q store.Querier, r *madeRow, change func(map[string]json.RawMessage)) error {
	value, fields := r.value, r.fields
	for attempt := 0; attempt < 8; attempt++ {
		change(fields)
		next, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		res, err := q.Exec(ctx, `UPDATE server_config SET value = ? WHERE key = ? AND value = ?`, string(next), r.key, value)
		if err != nil {
			return fmt.Errorf("end %s: %w", r.key, err)
		}
		if n, err := res.RowsAffected(); err != nil || n > 0 {
			return err
		}
		if err := q.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, r.key).Scan(&value); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		fields = nil
		if err := json.Unmarshal([]byte(value), &fields); err != nil {
			return nil
		}
	}
	return fmt.Errorf("end %s: it keeps changing", r.key)
}

// walkTable counts, and with end ends, what the tokens in want made in a
// table with a created_by_token column: the device tokens that are not
// revoked or expired, and the organization invitations nobody used.
func walkTable(ctx context.Context, q store.Querier, want map[string]bool, now time.Time, end bool, table string, of func(string) *Ending) error {
	if ok, err := hasColumn(ctx, q, table, "created_by_token"); err != nil || !ok {
		return err
	}
	var query string
	switch table {
	case "device_tokens":
		query = `SELECT created_by_token, id, expires_at FROM device_tokens WHERE created_by_token <> '' AND status NOT IN ('revoked', 'expired')`
	default:
		query = `SELECT created_by_token, id, expires_at FROM org_invitations WHERE created_by_token <> '' AND used_at IS NULL`
	}
	rows, err := q.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read %s: %w", table, err)
	}
	type row struct {
		token string
		id    any
		live  bool
	}
	var found []row
	for rows.Next() {
		var r row
		var expires sql.NullString
		if err := rows.Scan(&r.token, &r.id, &expires); err != nil {
			rows.Close()
			return err
		}
		if want != nil && !want[r.token] {
			continue
		}
		r.live = !expires.Valid || expires.String == "" || stillValid(expires.String, now)
		found = append(found, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range found {
		if end {
			var err error
			if table == "device_tokens" {
				_, err = q.Exec(ctx, `UPDATE device_tokens SET status = 'revoked', revoked_at = `+store.NowSQL(q.Dialect())+` WHERE id = ?`, r.id)
			} else {
				_, err = q.Exec(ctx, `DELETE FROM org_invitations WHERE id = ?`, r.id)
			}
			if err != nil {
				return fmt.Errorf("end a row of %s: %w", table, err)
			}
		}
		if !r.live {
			continue
		}
		if table == "device_tokens" {
			of(r.token).Ended.DeviceTokens++
		} else {
			of(r.token).Ended.OrganizationInvitations++
		}
	}
	return nil
}

// stillValid reports whether an expiry as a table keeps it (RFC 3339, or
// the time PostgreSQL writes as text) lies after now. A time it cannot
// read counts as valid: ending one too many is safe, counting it is not
// wrong.
func stillValid(text string, now time.Time) bool {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, text); err == nil {
			return now.Before(t)
		}
	}
	return true
}

// hasTable reports whether the database has the table: the signal
// server's tables are missing where only the console's were made (tests).
func hasTable(ctx context.Context, q store.Querier, table string) (bool, error) {
	var n int
	err := q.QueryRow(ctx, q.Pick(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?`), table).Scan(&n)
	return n > 0, err
}

// hasColumn reports whether the table has the column.
func hasColumn(ctx context.Context, q store.Querier, table, column string) (bool, error) {
	var n int
	err := q.QueryRow(ctx, q.Pick(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`,
		`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`), table, column).Scan(&n)
	return n > 0, err
}
