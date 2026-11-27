package securitymail

import (
	"context"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
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

// factsFunc writes a notice's facts in the mail's language; when writes a
// time in the recipient's time zone.
type factsFunc func(t brandmail.Translate, when func(time.Time) string) []brandmail.Fact

// signInNotice is the notice of a new sign-in.
func signInNotice(s SignIn) notice {
	return notice{
		key: KeyNewSignIn, kind: "security-signin", subject: "subject_signin", preheader: "preheader_signin",
		heading: "new_signin_heading", lead: "lead_signin", notYou: "not_you_signin",
		facts: func(t brandmail.Translate, when func(time.Time) string) []brandmail.Fact {
			out := []brandmail.Fact{{Label: t("mail.security.label_time", nil), Value: when(s.At)}}
			if s.Browser != "" {
				out = append(out, brandmail.Fact{Label: t("mail.security.label_browser", nil), Value: s.Browser})
			}
			if s.IP != "" {
				out = append(out, brandmail.Fact{Label: t("mail.security.label_address", nil), Value: s.IP})
			}
			return out
		},
	}
}

// NewSignIn tells the person that their account was signed in to from a
// browser it had not used before (switch KeyNewSignIn).
func (n *Notices) NewSignIn(ctx context.Context, userID int64, s SignIn) {
	if s.At.IsZero() {
		s.At = n.now()
	}
	n.send(ctx, userID, signInNotice(s))
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

func passwordNotice(how PasswordChange, at time.Time) notice {
	lead := "lead_password_self"
	switch how {
	case PasswordByLink:
		lead = "lead_password_link"
	case PasswordByAdmin:
		lead = "lead_password_admin"
	case PasswordTemporary:
		lead = "lead_password_temporary"
	}
	return notice{
		key: KeyPasswordChanged, kind: "security-password", subject: "subject_password", preheader: "preheader",
		heading: "password_heading", lead: lead, notYou: "not_you_password", facts: timeFact(at),
	}
}

// PasswordChanged tells the person that their password changed (switch
// KeyPasswordChanged).
func (n *Notices) PasswordChanged(ctx context.Context, userID int64, how PasswordChange) {
	n.send(ctx, userID, passwordNotice(how, n.now()))
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

func twoFactorNotice(change TwoFactorChange, at time.Time) notice {
	return notice{
		key: KeyTwoFactorChanged, kind: "security-two-factor", subject: "subject_two_factor", preheader: "preheader",
		heading: "two_factor_heading", lead: "lead_two_factor_" + string(change), notYou: "not_you_two_factor", facts: timeFact(at),
	}
}

// TwoFactorChanged tells the person what changed about their two-factor
// sign-in (switch KeyTwoFactorChanged).
func (n *Notices) TwoFactorChanged(ctx context.Context, userID int64, change TwoFactorChange) {
	n.send(ctx, userID, twoFactorNotice(change, n.now()))
}

func emailNotice(oldAddress, newAddress string, at time.Time) notice {
	return notice{
		key: KeyEmailChanged, kind: "security-email", subject: "subject_email", preheader: "preheader",
		heading: "email_heading", lead: "lead_email", notYou: "not_you_email", to: oldAddress,
		facts: func(t brandmail.Translate, when func(time.Time) string) []brandmail.Fact {
			return []brandmail.Fact{
				{Label: t("mail.security.label_new_address", nil), Value: newAddress},
				{Label: t("mail.security.label_time", nil), Value: when(at)},
			}
		},
	}
}

// EmailChanged tells the old address that the account has a new one
// (switch KeyEmailChanged). An empty old address sends nothing.
func (n *Notices) EmailChanged(ctx context.Context, userID int64, oldAddress, newAddress string) {
	if oldAddress == "" {
		return
	}
	n.send(ctx, userID, emailNotice(oldAddress, newAddress, n.now()))
}

// timeFact is the row that says when a change happened.
func timeFact(at time.Time) factsFunc {
	return func(t brandmail.Translate, when func(time.Time) string) []brandmail.Fact {
		return []brandmail.Fact{{Label: t("mail.security.label_time", nil), Value: when(at)}}
	}
}
