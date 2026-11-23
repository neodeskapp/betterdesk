package console

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/setupguide"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// newSetupGuide builds the first-install guide on the server's own view
// of itself: the address clients get without a request, whether the
// console listens with TLS, the device count of the overview and whether
// single sign-on or LDAP is on.
func (s *Server) newSetupGuide(db *store.DB, api *goapi.Client) *setupguide.Service {
	return setupguide.New(setupguide.Options{
		DB:          db,
		Address:     func() string { return s.deps.ServerInfo.ResolveClientFacingHost(nil, "") },
		ServesHTTPS: func() bool { return s.net.cert != nil },
		Devices: func(ctx context.Context) (int, error) {
			stats, err := s.deps.Backend.GetStats(ctx)
			if err != nil {
				return 0, err
			}
			// The counts come from the API server or the console's table,
			// in two shapes with the same keys
			raw, err := json.Marshal(stats)
			if err != nil {
				return 0, fmt.Errorf("device counts: %w", err)
			}
			var counts struct {
				Total float64 `json:"total"`
			}
			if err := json.Unmarshal(raw, &counts); err != nil {
				return 0, fmt.Errorf("device counts: %w", err)
			}
			return int(counts.Total), nil
		},
		DirectorySignIn: func(ctx context.Context) bool {
			return api.GetOIDCConfig(ctx).Object("data").Bool("enabled") ||
				api.GetLDAPConfig(ctx).Object("data").Bool("enabled")
		},
	})
}
