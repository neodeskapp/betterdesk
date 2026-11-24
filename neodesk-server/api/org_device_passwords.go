package api

import (
	"errors"
	"log"

	"github.com/neodeskapp/neodesk/neodesk-server/crypto/devicevault"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// Organizations' device passwords (the address book's stored passwords)
// are sealed with the key file of the device secrets (deviceVault), for
// their organization and device. Until this change they were sealed with a
// key derived from ORG_PEER_VAULT_KEY, else from the JWT secret, which the
// database itself holds (key id v1): a copy of the database or a full
// backup opened them. MigrateOrgPeerCredentials moves those rows at each
// start. ORG_PEER_VAULT_KEY, when set, is that old key and opens nothing
// else; it does not choose the new key, because the full backup can hold
// the console's .env, and a key from there would open the passwords again.

// errNoOrgVault is a server that can neither seal nor open organizations'
// device passwords.
var errNoOrgVault = errors.New("no key for organizations' device passwords")

// orgPasswordsAvailable reports whether organizations' device passwords
// can be stored.
func (s *Server) orgPasswordsAvailable() bool {
	return s.deviceVault != nil
}

// sealOrgPassword seals the device password an organization stores for
// peerID with the key file.
func (s *Server) sealOrgPassword(orgID, peerID, password string) (nonce, ciphertext, keyID string, err error) {
	if s.deviceVault == nil {
		return "", "", "", errNoOrgVault
	}
	nonce, ciphertext, err = s.deviceVault.SealOrgPassword(orgID, peerID, password)
	return nonce, ciphertext, devicevault.KeyID, err
}

// openOrgPassword opens a stored device password: with the key file, or
// for a row still sealed with the old key (restored from an older backup
// after this start; the next start moves it) with the old key.
func (s *Server) openOrgPassword(row *db.OrgPeerCredential) (string, error) {
	if row == nil {
		return "", errNoOrgVault
	}
	if row.KeyID == devicevault.KeyID {
		if s.deviceVault == nil {
			return "", errNoOrgVault
		}
		return s.deviceVault.OpenOrgPassword(row.OrgID, row.PeerID, row.Nonce, row.Ciphertext, row.KeyID)
	}
	if s.peerVault == nil {
		return "", errNoOrgVault
	}
	return s.peerVault.Open(row.Nonce, row.Ciphertext, row.KeyID)
}

// MigrateOrgPeerCredentials moves every organization's device password
// that is not sealed with the key file yet: opened with the old key,
// sealed with the key file for its organization and device, all in one
// transaction. A row the old key does not open stays as it was. It
// returns how many rows moved and how many stayed; a second run moves
// nothing. Without the key file it does nothing.
func (s *Server) MigrateOrgPeerCredentials() (moved, left int, err error) {
	if s.deviceVault == nil || s.db == nil {
		return 0, 0, nil
	}
	rows, err := s.db.ListOrgPeerCredentialsNotSealedWith(devicevault.KeyID)
	if err != nil {
		return 0, 0, err
	}
	var reseal []db.OrgPeerCredentialReseal
	for _, row := range rows {
		if s.peerVault == nil {
			left++
			continue
		}
		plain, err := s.peerVault.Open(row.Nonce, row.Ciphertext, row.KeyID)
		if err != nil {
			log.Printf("[org] the device password of %s in organization %s does not open with the old key; left as it was", row.PeerID, row.OrgID)
			left++
			continue
		}
		nonce, ciphertext, err := s.deviceVault.SealOrgPassword(row.OrgID, row.PeerID, plain)
		if err != nil {
			return 0, 0, err
		}
		reseal = append(reseal, db.OrgPeerCredentialReseal{
			OrgID: row.OrgID, PeerID: row.PeerID, OldKeyID: row.KeyID,
			Nonce: nonce, Ciphertext: ciphertext, KeyID: devicevault.KeyID,
		})
	}
	moved, err = s.db.ResealOrgPeerCredentials(reseal)
	if err != nil {
		return 0, 0, err
	}
	return moved, left, nil
}
