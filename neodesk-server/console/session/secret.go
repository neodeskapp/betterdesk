package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// secretFile is the file in the data directory that keeps a generated
// secret across restarts.
const secretFile = ".session_secret"

// lastGoodFile keeps a copy of the last secret the file held that could
// be used, for a start whose file holds none.
const lastGoodFile = ".session_secret.last-good"

// LoadSecret finds the secret that signs the session cookie and the CSRF
// token:
//
//  1. SESSION_SECRET from the environment, when it is not empty.
//  2. The file .session_secret in dataDir, without white space around it.
//  3. A new secret of 32 random bytes written as 64 hex characters. It is
//     saved to that file (mode 0600, no line break at the end) so the next
//     start finds it. When saving fails the secret is still returned, and
//     it lasts until the process ends.
//
// A file that holds no usable secret (empty, or with a control character
// such as NUL) does not stop the start: the last secret the file held
// that could be used is taken, else a new one, and written back to the
// file (L399).
func LoadSecret(getenv func(string) string, dataDir string) (string, error) {
	secret, _, err := LoadSecretNotice(getenv, dataDir)
	return secret, err
}

// LoadSecretNotice is LoadSecret that also says, in notice, when the
// file held no usable secret and what the console started with instead.
// The notice is logged too.
func LoadSecretNotice(getenv func(string) string, dataDir string) (secret, notice string, err error) {
	if secret := getenv("SESSION_SECRET"); secret != "" {
		return secret, "", nil
	}
	path := filepath.Join(dataDir, secretFile)
	// A file that cannot be looked at counts as missing; one that is there
	// and cannot be read is an error
	if _, err := os.Stat(path); err == nil {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("read session secret %s: %w", path, err)
		}
		secret := strings.Trim(string(content), jsWhitespace)
		if usableSecret(secret) {
			rememberGood(dataDir, secret)
			return secret, "", nil
		}
		if good := lastGood(dataDir); usableSecret(good) {
			notice = "The console session secret in " + path + " is empty or holds a control character. The console started with the last secret that worked and wrote it back to the file."
			if err := saveSecret(dataDir, path, good); err != nil {
				notice += " Writing it back failed: " + err.Error()
			}
			logging.Logf("session", "WARN: %s", notice)
			return good, notice, nil
		}
		notice = "The console session secret in " + path + " is empty or holds a control character. The console started with a new secret and wrote it to the file; everyone signs in again."
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate session secret: %w", err)
	}
	secret = hex.EncodeToString(raw)
	if err := saveSecret(dataDir, path, secret); err != nil {
		logging.Logf("session", "WARN: could not save the session secret, sessions end with this process: %v", err)
	} else {
		rememberGood(dataDir, secret)
	}
	if notice != "" {
		logging.Logf("session", "WARN: %s", notice)
	}
	return secret, notice, nil
}

// usableSecret reports whether a secret read from the file can sign
// sessions: not empty and without control characters.
func usableSecret(secret string) bool {
	if secret == "" {
		return false
	}
	for _, r := range secret {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// lastGood is the secret lastGoodFile keeps, "" for none.
func lastGood(dataDir string) string {
	content, err := os.ReadFile(filepath.Join(dataDir, lastGoodFile))
	if err != nil {
		return ""
	}
	return strings.Trim(string(content), jsWhitespace)
}

// rememberGood keeps a copy of a usable secret in lastGoodFile; a failure
// only means no copy.
func rememberGood(dataDir, secret string) {
	if lastGood(dataDir) == secret {
		return
	}
	if err := os.WriteFile(filepath.Join(dataDir, lastGoodFile), []byte(secret), 0o600); err != nil {
		logging.Logf("session", "WARN: could not keep a copy of the session secret: %v", err)
	}
}

func saveSecret(dataDir, path, secret string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(secret), 0o600)
}

// sign returns the signed form of a session id: the id, a dot, and the
// HMAC-SHA256 of the id under the secret in base64 without padding. The
// secret is used as text, not decoded from hex.
func sign(id, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	return id + "." + base64.RawStdEncoding.EncodeToString(mac.Sum(nil))
}

// unsign returns the session id in a signed value, and false unless the
// signature is the one sign makes for it.
func unsign(signed, secret string) (string, bool) {
	dot := strings.LastIndexByte(signed, '.')
	if dot < 0 {
		return "", false
	}
	id := signed[:dot]
	if subtle.ConstantTimeCompare([]byte(sign(id, secret)), []byte(signed)) != 1 {
		return "", false
	}
	return id, true
}

// cookiePrefix marks a signed cookie value.
const cookiePrefix = "s:"

// newID returns a new session id: 24 random bytes in URL-safe base64
// without padding, 32 characters.
func newID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
