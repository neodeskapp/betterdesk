package api

import (
	"encoding/json"
	"log"
	"time"
)

// tokenAdmissionPrefix is where the server notes a computer that was let
// in with a console API token: through an invite or a pairing code the
// token made, or an approval sent with it. The key is
// token_admission_<token>_<device>, the value {token, device_id, how, at}.
// The token's revoke dialog lists them for the admin to check; the end of
// the token lets none of them out (console/admintoken).
const tokenAdmissionPrefix = "token_admission_"

// The ways a computer is let in with a token.
const (
	admittedByInvite      = "invite"
	admittedByPairingCode = "pairing_code"
	admittedByApproval    = "approval"
	admittedByKeyApproval = "key_approval"
)

// noteTokenAdmission records that the computer deviceID was let in with
// the console API token token ("" for none: nothing is recorded).
func (s *Server) noteTokenAdmission(token, deviceID, how string) {
	if token == "" || deviceID == "" {
		return
	}
	data, err := json.Marshal(map[string]string{"token": token, "device_id": deviceID, "how": how, "at": time.Now().UTC().Format(time.RFC3339)})
	if err == nil {
		err = s.db.SetConfig(tokenAdmissionPrefix+token+"_"+deviceID, string(data))
	}
	if err != nil {
		log.Printf("[API] note the admission of %s with an API token: %v", deviceID, err)
	}
}
