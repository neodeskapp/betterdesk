package brandmail

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
)

// Of is the branding of the console's mails: its name, the accent colour
// of its appearance and the logo an admin uploaded. Without a branding
// service it is NeoDesk in the brand's colours. Host and Contact are the
// mail service's to fill.
func Of(b *branding.Service) Branding {
	out := Branding{Server: DefaultServer}
	if b == nil {
		return out
	}
	out.Server = b.AppName(DefaultServer)
	if appearance, err := b.GetPublicAppearance(); err == nil && appearance != nil {
		if obj, _ := appearance.Get("palette"); obj != nil {
			if o, ok := obj.(*branding.Object); ok {
				v, _ := o.Get("primary")
				s, _ := v.(string)
				out.Accent = strings.TrimSpace(s)
			}
		}
	}
	out.LogoImage, out.LogoType, _ = b.MailLogo()
	return out
}

// HostOf is the host part of an address ("https://a.example:8443/x" is
// "a.example:8443").
func HostOf(base string) string {
	if i := strings.Index(base, "://"); i >= 0 {
		base = base[i+3:]
	}
	if i := strings.IndexByte(base, '/'); i >= 0 {
		base = base[:i]
	}
	return base
}
