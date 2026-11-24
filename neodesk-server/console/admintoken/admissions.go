package admintoken

// The computers a token let in: joined through an invite or a pairing
// code it made, or approved (a registration, an enrollment, a new key)
// with it. The API server notes most of them (api/token_admissions.go),
// the console the approvals of registrations; both under
// token_admission_<token>_<device> in server_config. The end of a token
// lets none of them out: the revoke dialog lists them so the admin can
// check them.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const admissionPrefix = "token_admission_"

// The ways a computer is let in with a token.
const (
	AdmittedByInvite      = "invite"
	AdmittedByPairingCode = "pairing_code"
	AdmittedByApproval    = "approval"
	AdmittedByKeyApproval = "key_approval"
)

// Admission is a computer a token let in.
type Admission struct {
	DeviceID string `json:"device_id"`
	// Name is the computer's name as the server has it, "" for none.
	Name string `json:"name"`
	How  string `json:"how"`
	At   string `json:"at"`
}

// NoteAdmission records that the token let the computer deviceID in. A
// request without a token ("") records nothing.
func (s *Service) NoteAdmission(ctx context.Context, token, deviceID, how string) error {
	if token == "" || deviceID == "" {
		return nil
	}
	if ok, err := hasTable(ctx, s.db, "server_config"); err != nil || !ok {
		return err
	}
	data, err := json.Marshal(map[string]string{"token": token, "device_id": deviceID, "how": how, "at": s.now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO server_config (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		admissionPrefix+token+"_"+deviceID, string(data)); err != nil {
		return fmt.Errorf("note an admission with a token: %w", err)
	}
	return nil
}

// noted is an admission as server_config keeps it.
type noted struct {
	Token    string `json:"token"`
	DeviceID string `json:"device_id"`
	How      string `json:"how"`
	At       string `json:"at"`
}

// Admissions lists the computers the tokens ids let in that the server
// still has, the oldest first.
func (s *Service) Admissions(ctx context.Context, ids []string) (map[string][]Admission, error) {
	out := map[string][]Admission{}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	if len(want) == 0 {
		return out, nil
	}
	if ok, err := hasTable(ctx, s.db, "server_config"); err != nil || !ok {
		return out, err
	}
	rows, err := s.db.Query(ctx, `SELECT value FROM server_config WHERE key LIKE 'token\_admission\_%' ESCAPE '\'`)
	if err != nil {
		return nil, fmt.Errorf("read admissions: %w", err)
	}
	var found []noted
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var n noted
		if json.Unmarshal([]byte(raw), &n) == nil && want[n.Token] && n.DeviceID != "" {
			found = append(found, n)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	peers, err := hasTable(ctx, s.db, "peers")
	if err != nil {
		return nil, err
	}
	for _, n := range found {
		a := Admission{DeviceID: n.DeviceID, How: n.How, At: n.At}
		if peers {
			// A computer deleted since is no longer listed
			var name string
			if err := s.db.QueryRow(ctx, `SELECT COALESCE(NULLIF(display_name, ''), hostname, '') FROM peers WHERE id = ?`, n.DeviceID).Scan(&name); err != nil {
				continue
			}
			a.Name = name
		}
		out[n.Token] = append(out[n.Token], a)
	}
	for _, list := range out {
		sort.Slice(list, func(i, j int) bool {
			return list[i].At < list[j].At || (list[i].At == list[j].At && list[i].DeviceID < list[j].DeviceID)
		})
	}
	return out, nil
}
