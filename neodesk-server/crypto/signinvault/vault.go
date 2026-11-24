// Package signinvault encrypts the saved sign-in details of devices (the
// account name and password of a computer's operating system account) at
// rest with AES-256-GCM.
//
// The key is 32 random bytes in its own file, KeyFileName in the folder of
// the server's key pair, made with mode 0600 at the first start. It is not
// derived from the JWT secret or anything else the database holds, and the
// full backup does not include the file, so a copy of the database or of
// a backup holds the sealed details without the key that opens them.
//
// Every value is sealed for one device and one field: the device ID and
// the field name are the additional data, so a sealed password cannot be
// moved to another device's row or swapped with the account name by
// someone who can write the database but cannot read the key.
//
// Nothing here logs a value.
package signinvault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// KeyFileName is the key's file in the folder of the server's key pair.
const KeyFileName = ".signin_vault_key"

// KeyID names the key a row was sealed with.
const KeyID = "s1"

// Fields of a saved sign-in.
const (
	FieldUsername = "username"
	FieldPassword = "password"
)

const keySize = 32

// Vault seals and opens sign-in details.
type Vault struct {
	aead cipher.AEAD
}

// Load reads the key from KeyFileName in dir, or makes the file with a new
// random key when there is none.
func Load(dir string) (*Vault, error) {
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, KeyFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = create(path)
	}
	if err != nil {
		return nil, fmt.Errorf("signinvault: key file: %w", err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != keySize {
		return nil, fmt.Errorf("signinvault: %s does not hold a %d-byte key in hex", path, keySize)
	}
	return FromKey(key)
}

// create writes a new key into path, unless another process made the
// file first; then it reads that one.
func create(path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	text := []byte(hex.EncodeToString(key) + "\n")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(text); err != nil {
		f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return text, nil
}

// FromKey makes a vault from a 32-byte key.
func FromKey(key []byte) (*Vault, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("signinvault: the key has %d bytes, want %d", len(key), keySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// additional binds a sealed value to its device and field.
func additional(deviceID, field string) []byte {
	return []byte("neodesk sign-in " + KeyID + "\x00" + field + "\x00" + deviceID)
}

// Seal encrypts value for the field of deviceID and returns the nonce and
// the ciphertext in unpadded base64url.
func (v *Vault) Seal(deviceID, field, value string) (nonce, ciphertext string, err error) {
	if v == nil || v.aead == nil {
		return "", "", errors.New("signinvault: no key")
	}
	n := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, n); err != nil {
		return "", "", err
	}
	sealed := v.aead.Seal(nil, n, []byte(value), additional(deviceID, field))
	return base64.RawURLEncoding.EncodeToString(n), base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open decrypts what Seal made for the same device and field. A value
// sealed for another device or field, with another key, or changed in any
// way does not open.
func (v *Vault) Open(deviceID, field, nonce, ciphertext, keyID string) (string, error) {
	if v == nil || v.aead == nil {
		return "", errors.New("signinvault: no key")
	}
	if keyID != KeyID {
		return "", fmt.Errorf("signinvault: unknown key id %q", keyID)
	}
	n, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil || len(n) != v.aead.NonceSize() {
		return "", errors.New("signinvault: bad nonce")
	}
	c, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", errors.New("signinvault: bad ciphertext")
	}
	plain, err := v.aead.Open(nil, n, c, additional(deviceID, field))
	if err != nil {
		return "", errors.New("signinvault: the value does not open with this key for this device")
	}
	return string(plain), nil
}
