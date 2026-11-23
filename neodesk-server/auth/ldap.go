// Package auth — LDAP authentication provider for NeoDesk server.
//
// Supports LDAP and Active Directory with:
//   - Bind + search authentication (service account binds, searches for user, then binds as user)
//   - Direct bind (user DN template, no service account needed)
//   - Group → role mapping (configurable AD/LDAP group → NeoDesk role)
//   - StartTLS and LDAPS
//   - Auto-provisioning of local user accounts on first LDAP login
//
// Every sign-in opens its own connection: connect (and TLS), the service
// account's bind, the search for the user, the user's bind, and the group
// search when the role map needs it.
package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

// LDAPConfig holds all LDAP provider settings.
// Stored in server_config table with "ldap." prefix.
type LDAPConfig struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`             // e.g. "ldap.example.com"
	Port           int    `json:"port"`             // 389 (LDAP), 636 (LDAPS)
	UseTLS         bool   `json:"use_tls"`          // true = LDAPS (port 636)
	StartTLS       bool   `json:"start_tls"`        // true = STARTTLS on plain port
	SkipTLSVerify  bool   `json:"skip_tls_verify"`  // skip certificate verification (dev only)
	BindDN         string `json:"bind_dn"`          // service account DN for search
	BindPassword   string `json:"bind_password"`    // service account password
	BaseDN         string `json:"base_dn"`          // search base, e.g. "dc=example,dc=com"
	UserFilter     string `json:"user_filter"`      // LDAP filter, e.g. "(sAMAccountName={{username}})"
	UserAttrID     string `json:"user_attr_id"`     // attribute for username (default: sAMAccountName)
	UserAttrEmail  string `json:"user_attr_email"`  // attribute for email (default: mail)
	UserAttrName   string `json:"user_attr_name"`   // attribute for display name (default: displayName)
	GroupBaseDN    string `json:"group_base_dn"`    // group search base (empty = same as BaseDN)
	GroupFilter    string `json:"group_filter"`     // group membership filter (default: "(member={{dn}})")
	GroupAttrName  string `json:"group_attr_name"`  // group name attribute (default: cn)
	DefaultRole    string `json:"default_role"`     // role for users without group mapping (default: viewer)
	GroupRoleMap   string `json:"group_role_map"`   // JSON: {"CN=Admins,DC=...": "admin", "CN=Ops,DC=...": "operator"}
	DirectBind     bool   `json:"direct_bind"`      // true = skip search, bind directly with DN template
	DirectBindDN   string `json:"direct_bind_dn"`   // DN template, e.g. "uid={{username}},ou=users,dc=example,dc=com"
	ConnTimeoutSec int    `json:"conn_timeout_sec"` // connection timeout (default: 10)
	AllowSignup    bool   `json:"allow_signup"`     // a person the directory accepts gets an account (default: true)
}

// LDAPResult represents the outcome of an LDAP authentication attempt.
type LDAPResult struct {
	Authenticated bool     // true if bind succeeded
	Username      string   // account name: the entry's id attribute, lower-cased and trimmed
	DisplayName   string   // from LDAP attribute
	Email         string   // from LDAP attribute
	Role          string   // mapped role from group membership
	DN            string   // full distinguished name
	Groups        []string // list of group DNs the user belongs to
	// GroupNames are the values of the Group name attribute of the groups
	// the group search found; role map lines can name a group by them
	GroupNames []string
}

// LDAPProvider manages LDAP authentication.
type LDAPProvider struct {
	mu     sync.RWMutex
	config *LDAPConfig
}

// NewLDAPProvider creates a new LDAP provider with the given config.
func NewLDAPProvider(cfg *LDAPConfig) *LDAPProvider {
	return &LDAPProvider{config: cfg}
}

// UpdateConfig replaces the LDAP configuration (thread-safe).
func (p *LDAPProvider) UpdateConfig(cfg *LDAPConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = cfg
}

// Config returns a copy of the current LDAP configuration.
func (p *LDAPProvider) Config() LDAPConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return LDAPConfig{}
	}
	return *p.config
}

// IsEnabled reports whether LDAP authentication is enabled.
func (p *LDAPProvider) IsEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config != nil && p.config.Enabled
}

// Authenticate attempts to authenticate a user against the LDAP server.
// Returns nil result and error if LDAP is unreachable or misconfigured.
// Returns result with Authenticated=false if credentials are invalid.
func (p *LDAPProvider) Authenticate(username, password string) (*LDAPResult, error) {
	return p.AuthenticateContext(context.Background(), username, password)
}

// AuthenticateContext is Authenticate within ctx: the connect and every
// request wait at most until ctx's deadline, and the connection closes
// when ctx ends, which ends a bind or search on its way. It returns ctx's
// error when ctx ended before the directory answered.
func (p *LDAPProvider) AuthenticateContext(ctx context.Context, username, password string) (*LDAPResult, error) {
	cfg := p.Config()
	if !cfg.Enabled {
		return nil, fmt.Errorf("ldap: provider is disabled")
	}

	if username == "" || password == "" {
		return &LDAPResult{Authenticated: false}, nil
	}

	conn, err := p.dial(ctx, &cfg)
	if err != nil {
		if ended := contextEnded(ctx); ended != nil {
			return nil, ended
		}
		return nil, fmt.Errorf("ldap: connect: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	var result *LDAPResult
	if cfg.DirectBind {
		result, err = p.authenticateDirectBind(conn, &cfg, username, password)
	} else {
		result, err = p.authenticateBindSearch(conn, &cfg, username, password)
	}
	if ended := contextEnded(ctx); ended != nil {
		return nil, ended
	}
	return result, err
}

// contextEnded is ctx's error once it ended or its deadline passed: a
// request whose own timeout is the time left can fail a moment before ctx
// reports its end.
func contextEnded(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

// TestConnection verifies connectivity and bind credentials.
func (p *LDAPProvider) TestConnection() error {
	cfg := p.Config()
	if !cfg.Enabled {
		return fmt.Errorf("ldap: provider is disabled")
	}

	conn, err := p.dial(context.Background(), &cfg)
	if err != nil {
		return fmt.Errorf("ldap: connect: %w", err)
	}
	defer conn.Close()

	// If using service account, test bind
	if !cfg.DirectBind && cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return fmt.Errorf("ldap: service account bind failed: %w", err)
		}
	}

	return nil
}

// dial establishes a connection to the LDAP server. The connect, the TLS
// handshake and every request wait at most the connection timeout
// (ConnTimeoutSec, 10 seconds without one), and never past ctx's
// deadline.
func (p *LDAPProvider) dial(ctx context.Context, cfg *LDAPConfig) (*ldapv3.Conn, error) {
	timeout := time.Duration(cfg.ConnTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Deadline = deadline
		if left := time.Until(deadline); left < timeout {
			timeout = max(left, time.Millisecond)
		}
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))

	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.SkipTLSVerify,
		ServerName:         cfg.Host,
	}

	scheme := "ldap://"
	if cfg.UseTLS {
		// LDAPS (TLS from the start)
		scheme = "ldaps://"
	}
	conn, err := ldapv3.DialURL(scheme+addr, ldapv3.DialWithDialer(dialer), ldapv3.DialWithTLSConfig(tlsConfig))
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(timeout)
	if !cfg.UseTLS && cfg.StartTLS {
		if err := conn.StartTLS(tlsConfig); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// authenticateDirectBind performs a direct bind with a DN template.
func (p *LDAPProvider) authenticateDirectBind(conn *ldapv3.Conn, cfg *LDAPConfig, username, password string) (*LDAPResult, error) {
	// The name goes into the template as a DN value, so that its commas,
	// plus signs and equals signs cannot add parts to the DN
	dn := strings.ReplaceAll(cfg.DirectBindDN, "{{username}}", ldapv3.EscapeDN(username))

	if err := conn.Bind(dn, password); err != nil {
		if ldapv3.IsErrorWithCode(err, ldapv3.LDAPResultInvalidCredentials) {
			return &LDAPResult{Authenticated: false}, nil
		}
		return nil, fmt.Errorf("ldap: direct bind: %w", err)
	}

	result := &LDAPResult{
		Authenticated: true,
		Username:      directoryAccountName("", username),
		DN:            dn,
		Role:          cfg.DefaultRole,
	}

	// Try to fetch user attributes
	p.fetchUserAttributes(conn, cfg, dn, result)

	// Resolve groups & role
	p.resolveGroups(conn, cfg, result)

	return result, nil
}

// authenticateBindSearch performs bind+search authentication.
func (p *LDAPProvider) authenticateBindSearch(conn *ldapv3.Conn, cfg *LDAPConfig, username, password string) (*LDAPResult, error) {
	// Step 1: Bind with service account
	if cfg.BindDN != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap: service account bind: %w", err)
		}
	}

	// Step 2: Search for the user
	filter := cfg.UserFilter
	if filter == "" {
		filter = "(sAMAccountName={{username}})"
	}
	filter = strings.ReplaceAll(filter, "{{username}}", ldapv3.EscapeFilter(username))

	attrID, attrEmail, attrName := ldapUserAttributes(cfg)

	searchReq := ldapv3.NewSearchRequest(
		cfg.BaseDN,
		ldapv3.ScopeWholeSubtree,
		ldapv3.NeverDerefAliases,
		1,  // size limit
		10, // time limit (seconds)
		false,
		filter,
		[]string{"dn", attrID, attrEmail, attrName, "memberOf"},
		nil,
	)

	sr, err := conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("ldap: user search: %w", err)
	}
	if len(sr.Entries) == 0 {
		return &LDAPResult{Authenticated: false}, nil
	}

	entry := sr.Entries[0]
	userDN := entry.DN

	// Step 3: Bind as the user to verify password
	if err := conn.Bind(userDN, password); err != nil {
		if ldapv3.IsErrorWithCode(err, ldapv3.LDAPResultInvalidCredentials) {
			return &LDAPResult{Authenticated: false}, nil
		}
		return nil, fmt.Errorf("ldap: user bind: %w", err)
	}

	result := &LDAPResult{
		Authenticated: true,
		Username:      directoryAccountName(entry.GetEqualFoldAttributeValue(attrID), username),
		DisplayName:   entry.GetAttributeValue(attrName),
		Email:         entry.GetAttributeValue(attrEmail),
		DN:            userDN,
		Role:          cfg.DefaultRole,
	}

	// Extract memberOf groups from user entry
	memberOf := entry.GetAttributeValues("memberOf")
	if len(memberOf) > 0 {
		result.Groups = memberOf
	}

	// Resolve role from group mapping
	p.resolveGroups(conn, cfg, result)

	return result, nil
}

// ldapUserAttributes returns the configured attributes of an entry's
// account name, e-mail address and display name, or their defaults.
func ldapUserAttributes(cfg *LDAPConfig) (id, email, name string) {
	id, email, name = cfg.UserAttrID, cfg.UserAttrEmail, cfg.UserAttrName
	if id == "" {
		id = "sAMAccountName"
	}
	if email == "" {
		email = "mail"
	}
	if name == "" {
		name = "displayName"
	}
	return id, email, name
}

// directoryAccountName is the name of the account a directory entry signs
// in as: the value of its id attribute, else the name as typed, both
// lower-cased and without spaces at the ends. A directory matches names
// without regard to case, so every spelling of a name is one account.
func directoryAccountName(idValue, typed string) string {
	name := strings.TrimSpace(idValue)
	if name == "" {
		name = strings.TrimSpace(typed)
	}
	return strings.ToLower(name)
}

// fetchUserAttributes reads attributes from the user's DN entry.
func (p *LDAPProvider) fetchUserAttributes(conn *ldapv3.Conn, cfg *LDAPConfig, dn string, result *LDAPResult) {
	attrID, attrEmail, attrName := ldapUserAttributes(cfg)

	searchReq := ldapv3.NewSearchRequest(
		dn,
		ldapv3.ScopeBaseObject,
		ldapv3.NeverDerefAliases,
		1, 5, false,
		"(objectClass=*)",
		[]string{attrID, attrEmail, attrName, "memberOf"},
		nil,
	)

	sr, err := conn.Search(searchReq)
	if err != nil {
		log.Printf("[LDAP] Warning: failed to fetch user attributes for %s: %v", dn, err)
		return
	}
	if len(sr.Entries) > 0 {
		entry := sr.Entries[0]
		result.Username = directoryAccountName(entry.GetEqualFoldAttributeValue(attrID), result.Username)
		result.DisplayName = entry.GetAttributeValue(attrName)
		result.Email = entry.GetAttributeValue(attrEmail)
		memberOf := entry.GetAttributeValues("memberOf")
		if len(memberOf) > 0 {
			result.Groups = memberOf
		}
	}
}

// resolveGroups maps LDAP groups to a NeoDesk role using the group_role_map.
// If no group mapping matches, the default role is used.
// If group_filter is set and groups are empty, performs a group search.
func (p *LDAPProvider) resolveGroups(conn *ldapv3.Conn, cfg *LDAPConfig, result *LDAPResult) {
	if cfg.DefaultRole == "" {
		result.Role = RoleViewer
	} else {
		result.Role = cfg.DefaultRole
	}

	// Parse group→role map
	roleMap := parseGroupRoleMap(cfg.GroupRoleMap)
	if len(roleMap) == 0 {
		return
	}

	// If we don't have groups yet and have a group filter, search for them
	if len(result.Groups) == 0 && cfg.GroupFilter != "" {
		groupBaseDN := cfg.GroupBaseDN
		if groupBaseDN == "" {
			groupBaseDN = cfg.BaseDN
		}
		// The search runs as the service account where there is one: the
		// connection is bound as the person now, and many directories
		// don't let people read group entries
		if !cfg.DirectBind && cfg.BindDN != "" {
			if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
				log.Printf("[LDAP] Warning: service account bind for the group search failed: %v", err)
			}
		}
		groupFilter := strings.ReplaceAll(cfg.GroupFilter, "{{dn}}", ldapv3.EscapeFilter(result.DN))
		groupFilter = strings.ReplaceAll(groupFilter, "{{username}}", ldapv3.EscapeFilter(result.Username))

		attrGroupName := cfg.GroupAttrName
		if attrGroupName == "" {
			attrGroupName = "cn"
		}

		searchReq := ldapv3.NewSearchRequest(
			groupBaseDN,
			ldapv3.ScopeWholeSubtree,
			ldapv3.NeverDerefAliases,
			100, 10, false,
			groupFilter,
			[]string{"dn", attrGroupName},
			nil,
		)

		sr, err := conn.Search(searchReq)
		if err != nil {
			log.Printf("[LDAP] Warning: group search failed: %v", err)
		} else {
			for _, entry := range sr.Entries {
				result.Groups = append(result.Groups, entry.DN)
				for _, name := range entry.GetEqualFoldAttributeValues(attrGroupName) {
					if name = strings.TrimSpace(name); name != "" {
						result.GroupNames = append(result.GroupNames, name)
					}
				}
			}
		}
	}

	// Map groups to roles — highest-privilege match wins. A line names a
	// group by its DN or CN, or by the value of the Group name attribute
	// of a group the search found
	bestLevel := -1
	take := func(mapRole string) {
		if level := RoleLevel(mapRole); level > bestLevel {
			bestLevel = level
			result.Role = mapRole
		}
	}
	for mapKey, mapRole := range roleMap {
		for _, groupDN := range result.Groups {
			if ldapGroupMatches(mapKey, groupDN) {
				take(mapRole)
			}
		}
		for _, name := range result.GroupNames {
			if strings.EqualFold(strings.TrimSpace(mapKey), name) {
				take(mapRole)
			}
		}
	}

	if len(result.Groups) > 0 {
		log.Printf("[LDAP] User %s: groups=%v mapped role=%s", result.Username, result.Groups, result.Role)
	}
}

// ldapCommonName is the value of the first CN of a DN, with escapes
// resolved; "" when the text is no DN or has no CN.
func ldapCommonName(dn string) string {
	parsed, err := ldapv3.ParseDN(dn)
	if err != nil {
		return ""
	}
	for _, rdn := range parsed.RDNs {
		for _, attr := range rdn.Attributes {
			if strings.EqualFold(attr.Type, "CN") {
				return attr.Value
			}
		}
	}
	return ""
}

// ldapGroupMatches checks whether a group_role_map key matches a memberOf DN.
// A key written as a DN of more than one part matches that DN only (case
// and spaces between the parts aside): a CN is unique only inside its
// container. A key that is a bare name or one CN=<name> matches the
// group's CN wherever the group is.
func ldapGroupMatches(mapKey, groupDN string) bool {
	mapKey = strings.TrimSpace(mapKey)
	groupDN = strings.TrimSpace(groupDN)
	if mapKey == "" || groupDN == "" {
		return false
	}
	group, groupErr := ldapv3.ParseDN(groupDN)
	if key, err := ldapv3.ParseDN(mapKey); err == nil && len(key.RDNs) > 1 {
		if groupErr != nil {
			return strings.EqualFold(mapKey, groupDN)
		}
		return key.EqualFold(group)
	}
	groupCN := ldapCommonName(groupDN)
	if groupCN == "" {
		return false
	}
	if keyCN := ldapCommonName(mapKey); keyCN != "" {
		return strings.EqualFold(keyCN, groupCN)
	}
	return strings.EqualFold(mapKey, groupCN)
}

// parseGroupRoleMap parses a JSON-like group→role mapping string.
// Format: "group_dn=role,group_dn=role" (simple) or JSON object.
func parseGroupRoleMap(raw string) map[string]string {
	result := make(map[string]string)
	if raw == "" {
		return result
	}

	// Format: "CN=Admins,DC=ex,DC=com=admin|CN=Ops,DC=ex,DC=com=operator"
	// Pipe separates entries; newlines are also accepted (Settings UI paste).
	// Commas inside DNs are preserved — only the last '=' splits DN from role.
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", "|")
	entries := strings.Split(raw, "|")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Find the last '=' which separates DN from role
		idx := strings.LastIndex(entry, "=")
		if idx <= 0 {
			continue
		}
		dn := strings.TrimSpace(entry[:idx])
		role := strings.TrimSpace(entry[idx+1:])
		if dn != "" && role != "" && ValidRole(role) {
			result[dn] = role
		}
	}

	return result
}
