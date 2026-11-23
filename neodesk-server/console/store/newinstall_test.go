package store_test

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// A database the console sets up for the first time is a new install. The
// next start on it (an update) is not.
func TestMigrateTellsANewInstall(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		if db.NewInstall() {
			t.Fatal("a handle that never migrated reports a new install")
		}
		mustMigrate(t, db)
		if !db.NewInstall() {
			t.Error("the first Migrate on an empty database is not a new install")
		}
		mustMigrate(t, db)
		if db.NewInstall() {
			t.Error("the start after the first one reports a new install")
		}
	})
}

// A new install stays one when the signal server made its own tables
// first. A database the Node console set up is an existing install.
func TestNewInstallByOrigin(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		if want := origin != storetest.NodeFirst; db.NewInstall() != want {
			t.Errorf("%s: NewInstall = %v, want %v", origin, db.NewInstall(), want)
		}
	})
}
