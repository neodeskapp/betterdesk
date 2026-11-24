package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/crypto/devicevault"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/relay"
)

// Saved sign-in (BUGS L469): the account name and password of a device's
// operating system account, sealed with crypto/devicevault, that the
// browser viewer types at the device's lock or sign-in screen.
//
// Every route here answers only the console in the same process
// (inProcessOnly), which refuses API tokens for the use and writes the
// audit entries. The password is write-only: it is set, replaced and
// cleared, and no answer but the one of the use route holds it. That one
// hands it out once per live relay session, to the console account whose
// own browser session it is (the relay ticket names the account, signal
// knows it from the panel proof), and to nobody without a session open.
//
// The viewer is the end of the session's encryption, so the server cannot
// type into the session itself; the readable password exists in the
// browser for as long as the viewer takes to type it.

// webRemoteInitiator is the initiator of every browser viewer session
// (signal's panelWebRemoteInitiatorID).
const webRemoteInitiator = "panel-web-remote"

// Forms of a sign-in screen.
const (
	signinFormPassword     = "password"
	signinFormNamePassword = "name_password"
)

// signinMaxLen is the longest account name and password, in characters.
const signinMaxLen = 256

// signinUseTTL is how long the server remembers that a session used its
// sign-in; a relay session ends long before.
const signinUseTTL = 7 * 24 * time.Hour

// InitDeviceVault loads (or makes) the key file of the device secrets
// (saved sign-in details and organizations' device passwords) in dir, the
// folder of the server's key pair.
func (s *Server) InitDeviceVault(dir string) error {
	v, err := devicevault.Load(dir)
	if err != nil {
		return err
	}
	s.deviceVault = v
	return nil
}

// signinUses remembers the sessions that used a saved sign-in: one use per
// session, also after a failed attempt, so a wrong password can't lock
// the account by retries.
type signinUses struct {
	mu   sync.Mutex
	used map[string]time.Time
}

// claim reports whether session had not used a sign-in yet, and marks it.
func (u *signinUses) claim(session string, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.used == nil {
		u.used = map[string]time.Time{}
	}
	for k, at := range u.used {
		if now.Sub(at) > signinUseTTL {
			delete(u.used, k)
		}
	}
	if _, ok := u.used[session]; ok {
		return false
	}
	u.used[session] = now
	return true
}

func (s *Server) signinRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/peers/{id}/signin", s.inProcessOnly(s.requireAnyPermission([]string{auth.PermDeviceSigninManage, auth.PermDeviceSigninUse}, s.scopedDevice(s.handleGetSignin))))
	mux.HandleFunc("PUT /api/peers/{id}/signin", s.inProcessOnly(s.requirePermission(auth.PermDeviceSigninManage, s.scopedDevice(s.handleSetSignin))))
	mux.HandleFunc("PATCH /api/peers/{id}/signin", s.inProcessOnly(s.requirePermission(auth.PermDeviceSigninManage, s.scopedDevice(s.handleChangeSignin))))
	mux.HandleFunc("DELETE /api/peers/{id}/signin", s.inProcessOnly(s.requirePermission(auth.PermDeviceSigninManage, s.scopedDevice(s.handleClearSignin))))
	mux.HandleFunc("POST /api/peers/{id}/signin/use", s.inProcessOnly(s.requirePermission(auth.PermDeviceSigninUse, s.requirePermission(auth.PermDeviceConnect, s.scopedDevice(s.handleUseSignin)))))
}

// signinStatus is what the reads show: never the password.
type signinStatus struct {
	Available bool   `json:"available"`
	Set       bool   `json:"set"`
	Username  string `json:"username"`
	Form      string `json:"form"`
	Auto      bool   `json:"auto"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Server) signinStatusOf(row *db.DeviceSignin) signinStatus {
	st := signinStatus{Available: s.deviceVault != nil, Form: signinFormPassword}
	if row == nil {
		return st
	}
	st.Set = true
	st.Form, st.Auto, st.UpdatedBy, st.UpdatedAt = row.Form, row.Auto, row.UpdatedBy, row.UpdatedAt
	if s.deviceVault != nil {
		if name, err := s.deviceVault.Open(row.DeviceID, devicevault.FieldUsername, row.UserNonce, row.UserCipher, row.KeyID); err == nil {
			st.Username = name
		}
	}
	return st
}

func (s *Server) loadSignin(w http.ResponseWriter, id string) (*db.DeviceSignin, bool) {
	row, err := s.db.GetDeviceSignin(id)
	if err != nil {
		log.Printf("[signin] read the saved sign-in of %s: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return nil, false
	}
	return row, true
}

// GET /api/peers/{id}/signin
func (s *Server) handleGetSignin(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	row, ok := s.loadSignin(w, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.signinStatusOf(row))
}

// typeable reports why text can't be typed at a sign-in screen, "" when it
// can: no control characters (a tab or a line break would move on or
// submit), and only characters of the Basic Multilingual Plane (the
// device types each UTF-16 unit as one key; a pair would arrive broken).
func typeable(text string) string {
	if !utf8.ValidString(text) {
		return "invalid_text"
	}
	for _, c := range text {
		if unicode.IsControl(c) {
			return "control_character"
		}
		if c > 0xFFFF {
			return "character_not_typeable"
		}
	}
	return ""
}

func signinBadRequest(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

// PUT /api/peers/{id}/signin with {username, password, form}: set or
// replace. The switch keeps its setting on a replace.
func (s *Server) handleSetSignin(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if s.deviceVault == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "vault_unavailable"})
		return
	}
	var body struct {
		Username string  `json:"username"`
		Password string  `json:"password"`
		Form     *string `json:"form"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		signinBadRequest(w, "invalid_json")
		return
	}
	username := strings.TrimSpace(body.Username)
	switch {
	case username == "":
		signinBadRequest(w, "username_required")
		return
	case utf8.RuneCountInString(username) > signinMaxLen:
		signinBadRequest(w, "username_too_long")
		return
	case body.Password == "":
		signinBadRequest(w, "password_required")
		return
	case utf8.RuneCountInString(body.Password) > signinMaxLen:
		signinBadRequest(w, "password_too_long")
		return
	}
	if why := typeable(username); why != "" {
		signinBadRequest(w, "username_"+why)
		return
	}
	if why := typeable(body.Password); why != "" {
		signinBadRequest(w, "password_"+why)
		return
	}
	old, ok := s.loadSignin(w, id)
	if !ok {
		return
	}
	row := &db.DeviceSignin{DeviceID: id, KeyID: devicevault.KeyID, Form: signinFormPassword}
	if old != nil {
		row.Form, row.Auto = old.Form, old.Auto
	}
	if body.Form != nil {
		if *body.Form != signinFormPassword && *body.Form != signinFormNamePassword {
			signinBadRequest(w, "invalid_form")
			return
		}
		row.Form = *body.Form
	}
	var err error
	if row.UserNonce, row.UserCipher, err = s.deviceVault.Seal(id, devicevault.FieldUsername, username); err == nil {
		row.PassNonce, row.PassCipher, err = s.deviceVault.Seal(id, devicevault.FieldPassword, body.Password)
	}
	if err != nil {
		log.Printf("[signin] seal the sign-in of %s: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	row.UpdatedAt = s.signinNow().UTC().Format(time.RFC3339)
	row.UpdatedBy = actorFromCtx(r)
	if err := s.db.SaveDeviceSignin(row); err != nil {
		log.Printf("[signin] save the sign-in of %s: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	st := s.signinStatusOf(row)
	writeJSON(w, http.StatusOK, struct {
		signinStatus
		Replaced bool `json:"replaced"`
	}{st, old != nil})
}

// PATCH /api/peers/{id}/signin with {auto, form}: the switch and the form
// of the sign-in screen, for details that are saved.
func (s *Server) handleChangeSignin(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Auto *bool   `json:"auto"`
		Form *string `json:"form"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		signinBadRequest(w, "invalid_json")
		return
	}
	if body.Auto == nil && body.Form == nil {
		signinBadRequest(w, "nothing_to_change")
		return
	}
	if body.Form != nil && *body.Form != signinFormPassword && *body.Form != signinFormNamePassword {
		signinBadRequest(w, "invalid_form")
		return
	}
	row, ok := s.loadSignin(w, id)
	if !ok {
		return
	}
	if row == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_set"})
		return
	}
	if body.Auto != nil {
		row.Auto = *body.Auto
	}
	if body.Form != nil {
		row.Form = *body.Form
	}
	row.UpdatedAt = s.signinNow().UTC().Format(time.RFC3339)
	row.UpdatedBy = actorFromCtx(r)
	if err := s.db.SaveDeviceSignin(row); err != nil {
		log.Printf("[signin] save the sign-in of %s: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, s.signinStatusOf(row))
}

// DELETE /api/peers/{id}/signin
func (s *Server) handleClearSignin(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	gone, err := s.db.DeleteDeviceSignin(id)
	if err != nil {
		log.Printf("[signin] clear the sign-in of %s: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	st := s.signinStatusOf(nil)
	writeJSON(w, http.StatusOK, struct {
		signinStatus
		Cleared bool `json:"cleared"`
	}{st, gone})
}

// signinLive is the relay session of uuid while it runs.
func (s *Server) signinLive(uuid string) (relay.LivePair, bool) {
	if s.signinLiveFn != nil {
		return s.signinLiveFn(uuid)
	}
	return s.relay.Live(uuid)
}

func (s *Server) signinNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// POST /api/peers/{id}/signin/use with {session, account}: the account
// name and password, once per live session, for the console account
// whose own viewer session to this device it is. account is the console
// account's id; the console sets it, never the browser.
func (s *Server) handleUseSignin(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	refuse := func(status int, code string) {
		writeJSON(w, status, map[string]string{"error": code})
	}
	var body struct {
		Session string `json:"session"`
		Account int64  `json:"account"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		refuse(http.StatusBadRequest, "invalid_json")
		return
	}
	session := strings.TrimSpace(body.Session)
	if session == "" || len(session) > 128 {
		refuse(http.StatusBadRequest, "session_required")
		return
	}
	if s.deviceVault == nil {
		refuse(http.StatusServiceUnavailable, "vault_unavailable")
		return
	}
	row, ok := s.loadSignin(w, id)
	if !ok {
		return
	}
	if row == nil {
		refuse(http.StatusNotFound, "not_set")
		return
	}
	pair, live := s.signinLive(session)
	if !live || pair.Target != id || pair.Initiator != webRemoteInitiator || pair.Account <= 0 || pair.Account != body.Account {
		refuse(http.StatusConflict, "session_not_open")
		return
	}
	if !s.signinUses.claim(session, s.signinNow()) {
		refuse(http.StatusConflict, "already_used")
		return
	}
	username, err := s.deviceVault.Open(id, devicevault.FieldUsername, row.UserNonce, row.UserCipher, row.KeyID)
	var password string
	if err == nil {
		password, err = s.deviceVault.Open(id, devicevault.FieldPassword, row.PassNonce, row.PassCipher, row.KeyID)
	}
	if err != nil {
		// Another server's key (a restored backup) or a changed row
		log.Printf("[signin] the saved sign-in of %s does not open with this server's key", id)
		refuse(http.StatusConflict, "cannot_open")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"username": username, "password": password, "form": row.Form})
}
