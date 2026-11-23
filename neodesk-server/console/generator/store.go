package generator

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The bundle and build tables (agent_bundles, agent_bundle_builds), the
// dbAdapter methods of the old console. Revoked is an integer on SQLite
// and a boolean on PostgreSQL; times are text on SQLite and timestamptz
// on PostgreSQL, and reach JSON in those forms (store.Time).

// Bundle is a row of agent_bundles.
type Bundle struct {
	BundleID             string
	Slug                 store.NullString
	Name                 string
	Branding             string
	BrandingHash         string
	ConfigFingerprint    string
	ClientCommit         store.NullString
	ActiveGenerationHash store.NullString
	ProductType          string
	Revoked              jsbind.Cell
	DownloadCount        jsbind.Cell
	CreatedAt            store.Time
	UpdatedAt            store.Time
}

// IsRevoked is !!row.revoked.
func (b *Bundle) IsRevoked() bool { return b.Revoked.Value().Truthy() }

// PublicID is the address part of the bundle.
func (b *Bundle) PublicID() string { return PublicBundleID(b.Slug.String, b.BundleID) }

// ActiveBuildHash is the generation whose builds the bundle hands out:
// the promoted one, else the current one.
func (b *Bundle) ActiveBuildHash() string {
	if b.ActiveGenerationHash.String != "" {
		return b.ActiveGenerationHash.String
	}
	return b.BrandingHash
}

const bundleColumns = `bundle_id, slug, name, branding, branding_hash, config_fingerprint, client_commit,
	active_generation_hash, product_type, revoked, download_count, created_at, updated_at`

func scanBundles(rows *store.Rows) ([]*Bundle, error) {
	defer rows.Close()
	var out []*Bundle
	for rows.Next() {
		var b Bundle
		var slugText, name, branding, hash, fingerprint, product store.NullString
		if err := rows.Scan(&b.BundleID, &slugText, &name, &branding, &hash, &fingerprint, &b.ClientCommit,
			&b.ActiveGenerationHash, &product, &b.Revoked, &b.DownloadCount, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		b.Slug, b.Name, b.Branding, b.BrandingHash = slugText, name.String, branding.String, hash.String
		b.ConfigFingerprint, b.ProductType = fingerprint.String, product.String
		out = append(out, &b)
	}
	return out, rows.Err()
}

func (s *Service) bundles(ctx context.Context, query string, args ...any) ([]*Bundle, error) {
	rows, err := store.Select(ctx, s.opts.DB, query, args...)
	if err != nil {
		return nil, err
	}
	return scanBundles(rows)
}

func (s *Service) oneBundle(ctx context.Context, query string, args ...any) (*Bundle, error) {
	list, err := s.bundles(ctx, query, args...)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}

// ListBundles is listAgentBundles: newest first, revoked ones only when
// asked for.
func (s *Service) ListBundles(ctx context.Context, includeRevoked bool) ([]*Bundle, error) {
	where := ""
	if !includeRevoked {
		where = s.opts.DB.Pick("WHERE revoked = 0", "WHERE revoked = FALSE")
	}
	list, err := s.bundles(ctx, "SELECT "+bundleColumns+" FROM agent_bundles "+where+" ORDER BY created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("list agent bundles: %w", err)
	}
	return list, nil
}

// GetBundle is getAgentBundle.
func (s *Service) GetBundle(ctx context.Context, bundleID string) (*Bundle, error) {
	b, err := s.oneBundle(ctx, "SELECT "+bundleColumns+" FROM agent_bundles WHERE bundle_id = ?", bundleID)
	if err != nil {
		return nil, fmt.Errorf("load agent bundle %s: %w", bundleID, err)
	}
	return b, nil
}

// GetBundleByPublicID finds a bundle by its slug or its id.
func (s *Service) GetBundleByPublicID(ctx context.Context, publicID string) (*Bundle, error) {
	b, err := s.oneBundle(ctx, "SELECT "+bundleColumns+" FROM agent_bundles WHERE slug = ? OR bundle_id = ? LIMIT 1", publicID, publicID)
	if err != nil {
		return nil, fmt.Errorf("load agent bundle by public id: %w", err)
	}
	return b, nil
}

// IsSlugTaken reports a slug some other bundle than exclude has.
func (s *Service) IsSlugTaken(ctx context.Context, slug, exclude string) (bool, error) {
	if slug == "" {
		return false, nil
	}
	var owner string
	err := s.opts.DB.QueryRow(ctx, "SELECT bundle_id FROM agent_bundles WHERE slug = ? LIMIT 1", slug).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check agent bundle slug: %w", err)
	}
	if exclude != "" {
		return owner != exclude, nil
	}
	return true, nil
}

// NewBundle are the values of a new bundle row.
type NewBundle struct {
	BundleID          string
	Slug              string
	Name              string
	Branding          string
	BrandingHash      string
	ConfigFingerprint string
	ClientCommit      string
	CreatedBy         int64
	ProductType       string
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return dbText(s)
}

// CreateBundle is createAgentBundle and returns the new row.
func (s *Service) CreateBundle(ctx context.Context, n NewBundle) (*Bundle, error) {
	fingerprint := n.ConfigFingerprint
	if fingerprint == "" {
		fingerprint = n.BrandingHash
	}
	var createdBy any
	if n.CreatedBy != 0 {
		createdBy = n.CreatedBy
	}
	_, err := s.opts.DB.Exec(ctx, `INSERT INTO agent_bundles (
		bundle_id, slug, name, branding, branding_hash,
		config_fingerprint, client_commit, created_by, product_type
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.BundleID, nullIfEmpty(n.Slug), dbText(n.Name), dbText(n.Branding), n.BrandingHash,
		fingerprint, nullIfEmpty(n.ClientCommit), createdBy, NormalizeProductType(n.ProductType))
	if err != nil {
		return nil, fmt.Errorf("create agent bundle: %w", err)
	}
	return s.GetBundle(ctx, n.BundleID)
}

// BundleUpdate are the values updateAgentBundle writes.
type BundleUpdate struct {
	Name              string
	Slug              string
	Branding          string
	BrandingHash      string
	ConfigFingerprint string
	ClientCommit      string
}

// UpdateBundle is updateAgentBundle and returns the row, nil when it is
// gone.
func (s *Service) UpdateBundle(ctx context.Context, bundleID string, u BundleUpdate) (*Bundle, error) {
	fingerprint := u.ConfigFingerprint
	if fingerprint == "" {
		fingerprint = u.BrandingHash
	}
	_, err := s.opts.DB.Exec(ctx, `UPDATE agent_bundles
		SET name = ?, slug = ?, branding = ?, branding_hash = ?,
		    config_fingerprint = ?, client_commit = ?, updated_at = `+store.NowSQL(s.opts.DB.Dialect())+`
		WHERE bundle_id = ?`,
		dbText(u.Name), nullIfEmpty(u.Slug), dbText(u.Branding), u.BrandingHash, fingerprint, nullIfEmpty(u.ClientCommit), bundleID)
	if err != nil {
		return nil, fmt.Errorf("update agent bundle %s: %w", bundleID, err)
	}
	return s.GetBundle(ctx, bundleID)
}

// PrepareGeneration points a bundle at a new build generation without
// changing the one it hands out (prepareAgentBundleGeneration).
func (s *Service) PrepareGeneration(ctx context.Context, bundleID, brandingHash, configFingerprint, clientCommit string) (*Bundle, error) {
	if configFingerprint == "" {
		configFingerprint = brandingHash
	}
	_, err := s.opts.DB.Exec(ctx, `UPDATE agent_bundles
		SET branding_hash = ?, config_fingerprint = ?, client_commit = ?,
		    updated_at = `+store.NowSQL(s.opts.DB.Dialect())+`
		WHERE bundle_id = ?`, brandingHash, configFingerprint, nullIfEmpty(clientCommit), bundleID)
	if err != nil {
		return nil, fmt.Errorf("prepare agent bundle generation %s: %w", bundleID, err)
	}
	return s.GetBundle(ctx, bundleID)
}

// PromoteGeneration makes a finished generation the one every live bundle
// with that hash hands out.
func (s *Service) PromoteGeneration(ctx context.Context, brandingHash string) error {
	notRevoked := s.opts.DB.Pick("revoked = 0", "revoked = FALSE")
	_, err := s.opts.DB.Exec(ctx, `UPDATE agent_bundles
		SET active_generation_hash = ?, updated_at = `+store.NowSQL(s.opts.DB.Dialect())+`
		WHERE branding_hash = ? AND `+notRevoked, brandingHash, brandingHash)
	if err != nil {
		return fmt.Errorf("promote agent bundle generation: %w", err)
	}
	return nil
}

// SetRevoked is setAgentBundleRevoked and returns the row, nil when there
// is none.
func (s *Service) SetRevoked(ctx context.Context, bundleID string, revoked bool) (*Bundle, error) {
	var value any = 0
	if s.opts.DB.IsPostgres() {
		value = revoked
	} else if revoked {
		value = 1
	}
	_, err := s.opts.DB.Exec(ctx, "UPDATE agent_bundles SET revoked = ?, updated_at = "+store.NowSQL(s.opts.DB.Dialect())+" WHERE bundle_id = ?", value, bundleID)
	if err != nil {
		return nil, fmt.Errorf("revoke agent bundle %s: %w", bundleID, err)
	}
	return s.GetBundle(ctx, bundleID)
}

// DeleteBundle reports whether a row was deleted. Its builds stay.
func (s *Service) DeleteBundle(ctx context.Context, bundleID string) (bool, error) {
	res, err := s.opts.DB.Exec(ctx, "DELETE FROM agent_bundles WHERE bundle_id = ?", bundleID)
	if err != nil {
		return false, fmt.Errorf("delete agent bundle %s: %w", bundleID, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// IncrementDownload counts one download.
func (s *Service) IncrementDownload(ctx context.Context, bundleID string) error {
	_, err := s.opts.DB.Exec(ctx, "UPDATE agent_bundles SET download_count = download_count + 1, updated_at = "+store.NowSQL(s.opts.DB.Dialect())+" WHERE bundle_id = ?", bundleID)
	if err != nil {
		return fmt.Errorf("count agent bundle download: %w", err)
	}
	return nil
}

// Build is a row of agent_bundle_builds with its columns in the table's
// order, as the routes send it, and the fields the code reads.
type Build struct {
	columns []string
	values  []json.RawMessage

	BrandingHash      string
	Platform          string
	Arch              string
	Format            string
	Status            string
	ArtifactPath      string
	ArtifactSize      jsbind.Cell
	ArtifactSHA256    string
	ClientCommit      string
	GenerationID      string
	ConfigFingerprint string
	CreatedAt         store.Time
}

// Key is platform/arch/format.
func (b *Build) Key() string { return b.Platform + "/" + b.Arch + "/" + b.Format }

// Size is Number(row.artifact_size || 0).
func (b *Build) Size() float64 {
	v := b.ArtifactSize.Value()
	if !v.Truthy() {
		return 0
	}
	return v.ToNumber()
}

// MarshalJSON writes the row as the driver handed it to the old routes.
func (b *Build) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, column := range b.columns {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, _ := json.Marshal(column)
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(b.values[i])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

var buildTimeColumns = map[string]bool{"created_at": true, "updated_at": true, "started_at": true, "finished_at": true}
var buildIntColumns = map[string]bool{"id": true, "artifact_size": true, "progress_percent": true}

func (s *Service) builds(ctx context.Context, query string, args ...any) ([]*Build, error) {
	rows, err := store.Select(ctx, s.opts.DB, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []*Build{}
	for rows.Next() {
		dest := make([]any, len(columns))
		for i, c := range columns {
			switch {
			case buildTimeColumns[c]:
				dest[i] = new(store.Time)
			case buildIntColumns[c]:
				dest[i] = new(store.Int64String)
			default:
				dest[i] = new(jsbind.Cell)
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		b := &Build{columns: columns, values: make([]json.RawMessage, len(columns))}
		for i, c := range columns {
			raw, err := json.Marshal(dest[i])
			if err != nil {
				return nil, err
			}
			b.values[i] = raw
			text := ""
			if cell, ok := dest[i].(*jsbind.Cell); ok {
				if v := cell.Value(); v.IsString() {
					text = v.Str()
				}
			}
			switch c {
			case "branding_hash":
				b.BrandingHash = text
			case "platform":
				b.Platform = text
			case "arch":
				b.Arch = text
			case "format":
				b.Format = text
			case "status":
				b.Status = text
			case "artifact_path":
				b.ArtifactPath = text
			case "artifact_sha256":
				b.ArtifactSHA256 = text
			case "client_commit":
				b.ClientCommit = text
			case "generation_id":
				b.GenerationID = text
			case "config_fingerprint":
				b.ConfigFingerprint = text
			case "artifact_size":
				b.ArtifactSize = cellFromJSON(raw)
			case "created_at":
				b.CreatedAt = *dest[i].(*store.Time)
			}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// cellFromJSON is the value a column had for the old code, from its JSON:
// artifact_size is a number on SQLite and, as a PostgreSQL bigint, a
// string (node-postgres gave bigints as strings).
func cellFromJSON(raw json.RawMessage) jsbind.Cell {
	v, err := parseJSON(string(raw))
	if err != nil {
		return jsbind.CellOf(nil)
	}
	return jsbind.CellOf(v)
}

// ListBuildsForHash is listAgentBundleBuildsForHash.
func (s *Service) ListBuildsForHash(ctx context.Context, brandingHash string) ([]*Build, error) {
	list, err := s.builds(ctx, "SELECT * FROM agent_bundle_builds WHERE branding_hash = ? ORDER BY platform, arch, format", brandingHash)
	if err != nil {
		return nil, fmt.Errorf("list agent bundle builds: %w", err)
	}
	return list, nil
}

// GetBuild is getAgentBundleBuild.
func (s *Service) GetBuild(ctx context.Context, brandingHash, platform, arch, format string) (*Build, error) {
	list, err := s.builds(ctx, `SELECT * FROM agent_bundle_builds
		WHERE branding_hash = ? AND platform = ? AND arch = ? AND format = ?`, brandingHash, platform, arch, format)
	if err != nil {
		return nil, fmt.Errorf("load agent bundle build: %w", err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list[0], nil
}

// BuildUpdate is what upsertAgentBundleBuild writes. An empty artifact
// path, checksum, commit, generation or fingerprint keeps the stored one.
type BuildUpdate struct {
	BrandingHash      string
	Platform          string
	Arch              string
	Format            string
	Status            string
	ArtifactPath      string
	ArtifactSize      any
	ArtifactSHA256    string
	ErrorMessage      string
	ClientCommit      string
	GenerationID      string
	ConfigFingerprint string
	ProgressPercent   float64
	ProgressPhase     string
}

// UpsertBuild is upsertAgentBundleBuild: it sets the status (queued for
// both queue names), the start time when building and the end time when
// ready or failed.
func (s *Service) UpsertBuild(ctx context.Context, u BuildUpdate) error {
	status := normalizeBuildStatus(u.Status)
	size := u.ArtifactSize
	if !truthyGo(size) {
		size = int64(0)
	}
	percent := math.Max(0, math.Min(100, u.ProgressPercent))
	args := []any{u.BrandingHash, u.Platform, u.Arch, u.Format, status,
		nullIfEmpty(u.ArtifactPath), size, nullIfEmpty(u.ArtifactSHA256), dbText(u.ErrorMessage),
		nullIfEmpty(u.ClientCommit), nullIfEmpty(u.GenerationID), nullIfEmpty(u.ConfigFingerprint),
		int64(percent), dbText(u.ProgressPhase)}
	var query string
	if s.opts.DB.IsPostgres() {
		query = `INSERT INTO agent_bundle_builds (
			branding_hash, platform, arch, format, status,
			artifact_path, artifact_size, artifact_sha256, error_message,
			client_commit, generation_id, config_fingerprint,
			progress_percent, progress_phase,
			started_at, finished_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			CASE WHEN ?::text = 'building' THEN NOW() ELSE NULL END,
			CASE WHEN ?::text IN ('ready','failed') THEN NOW() ELSE NULL END
		)
		ON CONFLICT (branding_hash, platform, arch, format) DO UPDATE SET
			status = EXCLUDED.status,
			artifact_path = COALESCE(EXCLUDED.artifact_path, agent_bundle_builds.artifact_path),
			artifact_size = COALESCE(EXCLUDED.artifact_size, agent_bundle_builds.artifact_size),
			artifact_sha256 = COALESCE(EXCLUDED.artifact_sha256, agent_bundle_builds.artifact_sha256),
			error_message = EXCLUDED.error_message,
			client_commit = COALESCE(EXCLUDED.client_commit, agent_bundle_builds.client_commit),
			generation_id = COALESCE(EXCLUDED.generation_id, agent_bundle_builds.generation_id),
			config_fingerprint = COALESCE(EXCLUDED.config_fingerprint, agent_bundle_builds.config_fingerprint),
			progress_percent = EXCLUDED.progress_percent,
			progress_phase = EXCLUDED.progress_phase,
			started_at = CASE WHEN EXCLUDED.status = 'building' THEN NOW() ELSE agent_bundle_builds.started_at END,
			finished_at = CASE WHEN EXCLUDED.status IN ('ready','failed') THEN NOW() ELSE agent_bundle_builds.finished_at END,
			updated_at = NOW()`
		args = append(args, status, status)
	} else {
		started, finished := "NULL", "NULL"
		if status == "building" {
			started = "datetime('now')"
		}
		if status == "ready" || status == "failed" {
			finished = "datetime('now')"
		}
		query = `INSERT INTO agent_bundle_builds (
			branding_hash, platform, arch, format, status,
			artifact_path, artifact_size, artifact_sha256, error_message,
			client_commit, generation_id, config_fingerprint,
			progress_percent, progress_phase,
			started_at, finished_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ` + started + `, ` + finished + `)
		ON CONFLICT(branding_hash, platform, arch, format) DO UPDATE SET
			status = excluded.status,
			artifact_path = COALESCE(excluded.artifact_path, agent_bundle_builds.artifact_path),
			artifact_size = COALESCE(excluded.artifact_size, agent_bundle_builds.artifact_size),
			artifact_sha256 = COALESCE(excluded.artifact_sha256, agent_bundle_builds.artifact_sha256),
			error_message = excluded.error_message,
			client_commit = COALESCE(excluded.client_commit, agent_bundle_builds.client_commit),
			generation_id = COALESCE(excluded.generation_id, agent_bundle_builds.generation_id),
			config_fingerprint = COALESCE(excluded.config_fingerprint, agent_bundle_builds.config_fingerprint),
			progress_percent = excluded.progress_percent,
			progress_phase = excluded.progress_phase,
			started_at = CASE WHEN excluded.status = 'building' THEN datetime('now') ELSE agent_bundle_builds.started_at END,
			finished_at = CASE WHEN excluded.status IN ('ready','failed') THEN datetime('now') ELSE agent_bundle_builds.finished_at END,
			updated_at = datetime('now')`
	}
	if _, err := s.opts.DB.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("save agent bundle build %s/%s/%s: %w", u.Platform, u.Arch, u.Format, err)
	}
	return nil
}

// truthyGo is JavaScript truthiness of a Go value given as a size.
func truthyGo(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case int64:
		return t != 0
	case int:
		return t != 0
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	}
	return true
}
