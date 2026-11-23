package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// configFaultDB fails reads or writes of server_config.
type configFaultDB struct {
	db.Database
	getErr, setErr error
	sets           int
}

func (f *configFaultDB) GetConfig(key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.Database.GetConfig(key)
}

func (f *configFaultDB) SetConfig(key, value string) error {
	f.sets++
	if f.setErr != nil {
		return f.setErr
	}
	return f.Database.SetConfig(key, value)
}

func openMainTestDB(t *testing.T) db.Database {
	t.Helper()
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return database
}

func TestLoadJWTSecretKeepsTheStoredSecretWhenItCannotBeRead(t *testing.T) {
	database := openMainTestDB(t)
	if err := database.SetConfig("jwt_secret", "stored-secret"); err != nil {
		t.Fatal(err)
	}
	faulty := &configFaultDB{Database: database, getErr: errors.New("database is locked")}

	if secret, err := loadJWTSecret(faulty, ""); err == nil {
		t.Fatalf("loadJWTSecret = %q, want an error when the stored secret cannot be read", secret)
	}
	if faulty.sets != 0 {
		t.Fatalf("%d writes of server_config after a failed read, want none", faulty.sets)
	}
	if got, _ := database.GetConfig("jwt_secret"); got != "stored-secret" {
		t.Fatalf("stored secret = %q, want it unchanged", got)
	}
}

func TestLoadJWTSecretFailsWhenANewSecretCannotBeStored(t *testing.T) {
	faulty := &configFaultDB{Database: openMainTestDB(t), setErr: errors.New("disk full")}
	if secret, err := loadJWTSecret(faulty, ""); err == nil {
		t.Fatalf("loadJWTSecret = %q, want an error when the new secret cannot be stored", secret)
	}
}

func TestLoadJWTSecretStoresAndReusesItsSecret(t *testing.T) {
	database := openMainTestDB(t)
	first, err := loadJWTSecret(database, "")
	if err != nil || first == "" {
		t.Fatalf("first start: secret %q, err %v", first, err)
	}
	second, err := loadJWTSecret(database, "")
	if err != nil || second != first {
		t.Fatalf("second start: secret %q, err %v, want the stored one", second, err)
	}
	if got, err := loadJWTSecret(database, "from-env"); err != nil || got != "from-env" {
		t.Fatalf("configured secret: %q, %v", got, err)
	}
}
