package users

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/accountmail"
)

// The background job of the account life cycle runs every few minutes
// (Sweep): it disables the accounts whose end date has passed, tells the
// admins who may change an account a week before its end date, and runs
// the rule for inactive accounts (inactive.go). What it does is noted in
// the audit log with no acting account (user_id NULL) and the account as
// its target. It never disables the last Super admin that can sign in.

// sweepInterval is how often the job runs.
const sweepInterval = 5 * time.Minute

// ExpiryNotice is how long before its end date the admins hear of it.
const ExpiryNotice = 7 * 24 * time.Hour

// runSweeps runs Sweep every interval until ctx ends.
func runSweeps(ctx context.Context, d *kit.Deps) {
	tick := time.NewTicker(sweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if err := Sweep(ctx, d, time.Now()); err != nil && ctx.Err() == nil {
			d.Log.Error("Account life cycle:", err.Error())
		}
	}
}

// sweepAccount is an account as the job reads it.
type sweepAccount struct {
	ID        int64
	Username  string
	Role      string
	Email     string
	Provider  string
	Disabled  bool
	CreatedAt time.Time
	LastLogin time.Time
}

// sweepAccounts reads every account.
func sweepAccounts(ctx context.Context, d *kit.Deps) ([]sweepAccount, error) {
	accounts, err := allAccounts(ctx, d.Store)
	if err != nil {
		return nil, err
	}
	out := make([]sweepAccount, 0, len(accounts))
	for _, a := range accounts {
		email := ""
		if v := a.Email.Value(); v.IsString() {
			email = strings.TrimSpace(v.Str())
		}
		provider, _ := providerOf(a)
		created, _ := a.CreatedAt.Instant()
		last, _ := a.LastLogin.Instant()
		out = append(out, sweepAccount{
			ID: a.ID.V, Username: a.username(), Role: a.role(), Email: email, Provider: provider,
			Disabled: a.Disabled.True(), CreatedAt: created, LastLogin: last,
		})
	}
	return out, nil
}

// Sweep runs the job once at now.
func Sweep(ctx context.Context, d *kit.Deps, now time.Time) error {
	life := lifecycle.New(d.Store, func() time.Time { return now })
	accounts, err := sweepAccounts(ctx, d)
	if err != nil {
		return err
	}
	states, err := life.All(ctx)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		st := states[a.ID]
		if st.ExpiresAt.IsZero() || !st.ExpiredAt.IsZero() {
			continue
		}
		switch {
		case !now.Before(st.ExpiresAt):
			if err := expire(ctx, d, life, a, st, now); err != nil {
				return err
			}
		case st.ExpiresAt.Sub(now) <= ExpiryNotice && st.ExpiryNoticeAt.IsZero() && !a.Disabled:
			if err := expiryNotice(ctx, d, life, a, st, accounts, now); err != nil {
				return err
			}
		}
	}
	return sweepInactive(ctx, d, life, accounts, states, now)
}

// disableBySystem disables an account for the job: its sessions and
// access tokens end as they do for Disable.
func disableBySystem(ctx context.Context, d *kit.Deps, id int64) error {
	if err := setDisabled(ctx, d.Store, id, true); err != nil {
		return err
	}
	if _, err := d.Sessions.EndAccount(ctx, id, ""); err != nil {
		return err
	}
	return d.Authn.RevokeClientTokens(ctx, id, "", "")
}

// expire disables an account whose end date has passed.
func expire(ctx context.Context, d *kit.Deps, life *lifecycle.Store, a sweepAccount, st lifecycle.State, now time.Time) error {
	if !a.Disabled {
		last, err := isLastSuperAdmin(ctx, d.Store, a.ID, a.Role, now)
		if err != nil {
			return err
		}
		if last {
			// Checked again at every run: once another Super admin can
			// sign in, this one ends too
			d.Log.Warn("[users] " + a.Username + " reached its end date but is the last Super admin that can sign in; it stays enabled")
			return nil
		}
		if err := disableBySystem(ctx, d, a.ID); err != nil {
			return err
		}
		details := fmt.Sprintf("User %s reached its end date (%s UTC) and was disabled", a.Username, st.ExpiresAt.Format("2006-01-02 15:04"))
		if err := d.Audit.WriteAbout(ctx, 0, a.ID, "user_expired", details, ""); err != nil {
			return err
		}
	}
	return life.Update(ctx, a.ID, func(s *lifecycle.State) { s.ExpiredAt = now.UTC() })
}

// noticeRecipients are the accounts that may change target and hear of
// it: enabled, with an address, with user.edit, and allowed to change an
// account of its role (MayChangeAccount). The account itself is left out.
func noticeRecipients(ctx context.Context, d *kit.Deps, target sweepAccount, accounts []sweepAccount, now time.Time) ([]sweepAccount, error) {
	var out []sweepAccount
	for _, a := range accounts {
		if a.ID == target.ID || a.Disabled || a.Email == "" {
			continue
		}
		if gone, err := lifecycle.Expired(ctx, d.Store, a.ID, now); err != nil || gone {
			continue
		}
		edit, err := d.Access.RoleHasEffectivePermission(ctx, a.Role, "user.edit")
		if err != nil || !edit {
			continue
		}
		may, err := d.Access.MayChangeAccount(ctx, a.ID, a.Role, access.PolicyUser{ID: target.ID, Role: target.Role})
		if err != nil || !may {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// expiryNotice tells the admins once that an account ends within a week.
// Without mail settings nothing is sent and nothing noted, so the notice
// goes out once mail works.
func expiryNotice(ctx context.Context, d *kit.Deps, life *lifecycle.Store, a sweepAccount, st lifecycle.State, accounts []sweepAccount, now time.Time) error {
	if !accountmail.Ready(ctx, d) {
		return nil
	}
	admins, err := noticeRecipients(ctx, d, a, accounts, now)
	if err != nil {
		return err
	}
	base := accountmail.Base(d, "")
	brand := accountmail.Branding(d, base)
	for _, admin := range admins {
		t := accountmail.In(d, accountmail.LangOf(ctx, d, admin.ID))
		vars := map[string]any{"server": brand.Server, "username": a.Username, "date": people.TimeIn(st.ExpiresAt, people.TimeZone(ctx, d.Store, admin.ID)), "host": accountmail.Host(base)}
		page := accountmail.Page{
			Subject: t("account_mail.expiry_subject", vars), Heading: t("account_mail.expiry_heading", vars),
			Lead: t("account_mail.expiry_lead", vars), Small: []string{t("account_mail.expiry_why", vars)},
			Footer: t("password_link.mail_footer", vars),
		}
		if base != "" {
			page.Button, page.Link = t("account_mail.open_users", vars), base+"/users"
		}
		if _, err := accountmail.Send(ctx, d, admin.Email, accountmail.Build(brand, page)); err != nil {
			d.Log.Error("Expiry notice error:", err.Error())
		}
	}
	return life.Update(ctx, a.ID, func(s *lifecycle.State) { s.ExpiryNoticeAt = now.UTC() })
}
