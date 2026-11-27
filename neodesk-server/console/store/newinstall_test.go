package store_test

import (
	"bytes"
	"log"
	"strings"
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

// DOCKER-QUICK-13: setting the console up on a new database logs the
// tables it made, not every column and row it added to the server's
// tables a moment before; on a database the Node console set up, each
// added column is still logged.
func TestNewInstallLogsNoAddedColumns(t *testing.T) {
	for _, origin := range []storetest.Origin{storetest.GoServerFirst, storetest.NodeFirst} {
		t.Run(origin.String(), func(t *testing.T) {
			var buf bytes.Buffer
			prev := log.Writer()
			log.SetOutput(&buf)
			defer log.SetOutput(prev)
			storetest.Prepare(t, storetest.SQLite(t), origin)
			added := strings.Count(buf.String(), "Migration: added")
			if origin == storetest.GoServerFirst && added != 0 {
				t.Errorf("a new install logged %d added columns or rows:\n%s", added, buf.String())
			}
			if origin == storetest.NodeFirst && added == 0 {
				t.Errorf("an existing install logged no added column:\n%s", buf.String())
			}
		})
	}
}
