package securitymail

import (
	"context"
	"time"
)

// SignIn is what the notice of a new sign-in says about it.
type SignIn struct {
	// At is when it happened, IP the address it came from, Browser the
	// browser and system as the signed-in devices list names them
	// ("Firefox on Windows"), "" when unknown.
	At      time.Time
	IP      string
	Browser string
}

// NewSignIn tells the person that their account was signed in to from a
// browser it had not used before (switch KeyNewSignIn).
func (n *Notices) NewSignIn(ctx context.Context, userID int64, s SignIn) {
	at := s.At
	if at.IsZero() {
		at = n.now()
	}
	n.send(ctx, userID, notice{
		key: KeyNewSignIn, subject: "new_signin_subject", heading: "new_signin_heading", lead: "new_signin_lead",
		notYou: "not_you_signin",
		facts: func(t func(string, map[string]any) string) []string {
			out := []string{t("security_notice.fact_time", map[string]any{"time": at.UTC().Format("2006-01-02 15:04") + " UTC"})}
			if s.IP != "" {
				out = append(out, t("security_notice.fact_address", map[string]any{"address": s.IP}))
			}
			if s.Browser != "" {
				out = append(out, t("security_notice.fact_browser", map[string]any{"browser": s.Browser}))
			}
			return out
		},
	})
}

// How a password changed.
type PasswordChange string

const (
	// PasswordBySelf: the person changed it on Your account, or chose it
	// at sign-in when they had to.
	PasswordBySelf PasswordChange = "self"
	// PasswordByLink: the person set it with a reset link.
	PasswordByLink PasswordChange = "link"
	// PasswordByAdmin: an admin set a new password.
	PasswordByAdmin PasswordChange = "admin"
	// PasswordTemporary: an admin set a temporary password, which the
	// person replaces at the next sign-in.
	PasswordTemporary PasswordChange = "temporary"
)

// PasswordChanged tells the person that their password changed (switch
// KeyPasswordChanged).
func (n *Notices) PasswordChanged(ctx context.Context, userID int64, how PasswordChange) {
	lead := "password_lead_self"
	switch how {
	case PasswordByLink:
		lead = "password_lead_link"
	case PasswordByAdmin:
		lead = "password_lead_admin"
	case PasswordTemporary:
		lead = "password_lead_temporary"
	}
	n.send(ctx, userID, notice{
		key: KeyPasswordChanged, subject: "password_subject", heading: "password_heading", lead: lead,
		notYou: "not_you_password", facts: n.timeFact(),
	})
}

// What changed about two-factor sign-in.
type TwoFactorChange string

const (
	TwoFactorOn             TwoFactorChange = "on"
	TwoFactorOff            TwoFactorChange = "off"
	TwoFactorOffByAdmin     TwoFactorChange = "off_admin"
	TwoFactorReset          TwoFactorChange = "reset"
	RecoveryCodesUsedUp     TwoFactorChange = "codes_used_up"
	RecoveryCodesRenewed    TwoFactorChange = "codes_renewed"
	RecoveryCodesInvalidate TwoFactorChange = "codes_invalidated"
)

// TwoFactorChanged tells the person what changed about their two-factor
// sign-in (switch KeyTwoFactorChanged).
func (n *Notices) TwoFactorChanged(ctx context.Context, userID int64, change TwoFactorChange) {
	n.send(ctx, userID, notice{
		key: KeyTwoFactorChanged, subject: "two_factor_subject", heading: "two_factor_heading",
		lead: "two_factor_lead_" + string(change), notYou: "not_you_two_factor", facts: n.timeFact(),
	})
}

// EmailChanged tells the old address that the account has a new one
// (switch KeyEmailChanged). An empty old address sends nothing.
func (n *Notices) EmailChanged(ctx context.Context, userID int64, oldAddress, newAddress string) {
	if oldAddress == "" {
		return
	}
	n.send(ctx, userID, notice{
		key: KeyEmailChanged, subject: "email_subject", heading: "email_heading", lead: "email_lead",
		notYou: "not_you_email", to: oldAddress,
		facts: func(t func(string, map[string]any) string) []string {
			return []string{
				t("security_notice.fact_new_address", map[string]any{"address": newAddress}),
				t("security_notice.fact_time", map[string]any{"time": n.now().UTC().Format("2006-01-02 15:04") + " UTC"}),
			}
		},
	})
}

// timeFact is the line that says when a change happened.
func (n *Notices) timeFact() func(t func(string, map[string]any) string) []string {
	at := n.now()
	return func(t func(string, map[string]any) string) []string {
		return []string{t("security_notice.fact_time", map[string]any{"time": at.UTC().Format("2006-01-02 15:04") + " UTC"})}
	}
}
