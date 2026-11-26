package users

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/accountmail"
)

// The rule for inactive accounts (Settings, Sign-in; off by default): an
// account that has not signed in for the set number of days is disabled.
// Its inactivity counts from its last sign-in, or from when it was made
// when it never signed in, or from when an admin last enabled it if that
// is later.
//
// The person hears of it first: InactiveNoticeDays before, or at the
// first run that finds the account due, a mail goes to the account's
// address, and the account is disabled no sooner than a week after that
// mail. An account without an address, or a console that can't send
// mail, gets no notice and is disabled when it is due.
//
// Left alone: disabled accounts, accounts with the exception (Edit, "Keep
// this account even when it isn't used", for service accounts), pro and
// device accounts (they sign in through the desktop client, which the
// last sign-in does not always see), accounts whose invite is still open,
// and the last Super admin that can sign in.

// noticeLead is how long before the disable the notice goes out.
const noticeLead = lifecycle.InactiveNoticeDays * 24 * time.Hour

// inactiveSince is when the account's inactivity began; zero when it is
// not known.
func inactiveSince(a sweepAccount, st lifecycle.State) time.Time {
	since := a.LastLogin
	if since.IsZero() {
		since = a.CreatedAt
	}
	if st.ActiveFrom.After(since) {
		since = st.ActiveFrom
	}
	return since
}

func sweepInactive(ctx context.Context, d *kit.Deps, life *lifecycle.Store, accounts []sweepAccount, states map[int64]lifecycle.State, now time.Time) error {
	days := lifecycle.InactiveDays(ctx, d.Store)
	if days == 0 {
		return nil
	}
	invites, err := passwordlinks.New(d.Store, func() time.Time { return now }).Invites(ctx)
	if err != nil {
		return err
	}
	mailReady := accountmail.Ready(ctx, d)
	limit := time.Duration(days) * 24 * time.Hour
	for _, a := range accounts {
		st := states[a.ID]
		if a.Disabled || st.InactiveExempt || auth.IsProRole(a.Role) || auth.IsDeviceRole(a.Role) {
			continue
		}
		if inv, invited := invites[a.ID]; invited && a.LastLogin.IsZero() && !inv.Expired {
			continue
		}
		since := inactiveSince(a, st)
		if since.IsZero() {
			continue
		}
		due := since.Add(limit)
		if now.Before(due.Add(-noticeLead)) {
			continue
		}
		// A notice of this stretch of inactivity, not of one before it
		noticed := !st.InactiveNoticeAt.IsZero() && !st.InactiveNoticeAt.Before(since)
		canNotify := mailReady && a.Email != ""
		if canNotify && !noticed {
			disableOn := due
			if earliest := now.Add(noticeLead); earliest.After(disableOn) {
				disableOn = earliest
			}
			if err := inactiveNotice(ctx, d, a, days, disableOn); err != nil {
				d.Log.Error("Inactivity notice error:", err.Error())
				continue
			}
			if err := life.Update(ctx, a.ID, func(s *lifecycle.State) { s.InactiveNoticeAt = now.UTC() }); err != nil {
				return err
			}
			continue
		}
		if now.Before(due) || (noticed && now.Before(st.InactiveNoticeAt.Add(noticeLead))) {
			continue
		}
		last, err := isLastSuperAdmin(ctx, d.Store, a.ID, a.Role, now)
		if err != nil {
			return err
		}
		if last {
			continue
		}
		if err := disableBySystem(ctx, d, a.ID); err != nil {
			return err
		}
		when := "never signed in"
		if !a.LastLogin.IsZero() {
			when = "last signed in " + a.LastLogin.UTC().Format("2006-01-02")
		}
		details := fmt.Sprintf("Disabled user %s, which had not signed in for %d days (%s)", a.Username, days, when)
		if err := d.Audit.WriteAbout(ctx, 0, a.ID, "user_disabled_inactive", details, ""); err != nil {
			return err
		}
	}
	return nil
}

// inactiveNotice tells the person that the account will be disabled.
func inactiveNotice(ctx context.Context, d *kit.Deps, a sweepAccount, days int, on time.Time) error {
	base := accountmail.Base(d, "")
	brand := accountmail.Branding(d, base)
	t := accountmail.In(d, accountmail.LangOf(ctx, d, a.ID))
	vars := map[string]any{"server": brand.Server, "username": a.Username, "days": days, "date": on.UTC().Format("2006-01-02"), "host": accountmail.Host(base)}
	page := accountmail.Page{
		Subject: t("account_mail.inactive_subject", vars), Heading: t("account_mail.inactive_heading", vars),
		Lead: t("account_mail.inactive_lead", vars), Small: []string{t("account_mail.inactive_why", vars)},
		Footer: t("password_link.mail_footer", vars),
	}
	if base != "" {
		page.Button, page.Link = t("account_mail.inactive_button", vars), base+"/login"
	}
	sent, err := accountmail.Send(ctx, d, a.Email, accountmail.Build(brand, page))
	if err == nil && !sent {
		err = fmt.Errorf("the mail server didn't take the notice to %s", a.Username)
	}
	return err
}

// exemptOf reads the body's "inactive_exempt": change says it is there and
// differs from what the account has. It must be true or false.
func (h *handlers) exemptOf(r *http.Request, body middleware.JSONObject, user *account) (exempt, change bool, err error) {
	given := member(body, "inactive_exempt")
	if given.IsUndefined() {
		return false, false, nil
	}
	switch given.JSON() {
	case "true":
		exempt = true
	case "false":
	default:
		return false, false, say(http.StatusBadRequest, "Invalid value for inactive_exempt")
	}
	if isSessionAccount(r, user.ID.V) {
		return false, false, sayKey(r, http.StatusBadRequest, "users.exempt_own")
	}
	st, err := h.life.Get(r.Context(), user.ID.V)
	if err != nil {
		return false, false, err
	}
	return exempt, exempt != st.InactiveExempt, nil
}

// setExempt writes the exception and notes it in the audit log.
func (h *handlers) setExempt(r *http.Request, user *account, exempt bool) error {
	if err := h.life.Update(r.Context(), user.ID.V, func(s *lifecycle.State) { s.InactiveExempt = exempt }); err != nil {
		return err
	}
	details := "Put user " + user.username() + " under the rule for inactive accounts again"
	if exempt {
		details = "Kept user " + user.username() + " from the rule for inactive accounts"
	}
	return h.d.LogAccountAction(r, user.ID.V, "user_inactive_exempt_set", details)
}
