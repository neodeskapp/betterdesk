package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestDeletingAnUnknownOrganizationIs404: DELETE of an organization that
// doesn't exist says so instead of answering 204 (BUGS L396).
func TestDeletingAnUnknownOrganizationIs404(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateOrganization(&db.Organization{ID: "org-d", Name: "Doomed", Slug: "doomed", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if status, answer := asConsole(t, srv, http.MethodDelete, "/api/org/no-such-org", ""); status != http.StatusNotFound {
		t.Errorf("DELETE of a missing organization: %d %s, want 404", status, answer)
	}
	if status, answer := asConsole(t, srv, http.MethodDelete, "/api/org/org-d", ""); status != http.StatusNoContent {
		t.Errorf("DELETE of an organization: %d %s, want 204", status, answer)
	}
}
