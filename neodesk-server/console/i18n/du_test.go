package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// formalAddress finds the German forms that address the reader formally:
// Sie and the capitalised Ihr, Ihre, Ihren, Ihrem, Ihrer, Ihres, Ihnen.
// Lowercase sie and ihr are third person and never match.
var formalAddress = regexp.MustCompile(`(?:^|[^\p{L}])(Sie|Ihr|Ihre|Ihren|Ihrem|Ihrer|Ihres|Ihnen)(?:[^\p{L}]|$)`)

// theyNotYou lists the German texts where a capitalised Sie or Ihre starts
// a sentence and means they, it or their, not you. Each entry is the key and the
// words of its text that hold the Sie, with what Sie stands for.
var theyNotYou = []struct{ key, words, reason string }{
	{"settings.public_endpoints_desc", "Sie erscheinen in der Client-Einrichtung", "the addresses"},
	{"settings.appearance.refused_summary", "Sie sind auf der Seite markiert", "the values"},
	{"settings.backup_tab.restore_json_note", "Sie stellt Konsoleneinstellungen", "the settings file"},
	{"generator.download_link_hint", "Sie sehen eine gebrandete Download-Seite", "the end users"},
	{"branding.builtin_themes_hint", "Sie sind vom schnellen Hell-/Dunkel-Schalter getrennt", "the presets"},
	{"device_detail.usage_off", "Sie sind dort ausgeschaltet", "the usage data"},
	{"inventory.detail_none", "Sie erscheinen, sobald der NeoDesk-Agent", "the hardware details"},
	{"activity.empty", "Sie erscheint hier", "the app usage"},
	{"automation.no_alerts_hint", "Sie erscheinen hier", "the alerts"},
	{"automation.severity_hint", "Sie ändert nichts am Ablauf", "the label"},
	{"automation.rule_disabled_toast", "Sie löst keine Alarme aus", "the rule"},
	{"automation.custom_not_checked", "Sie löst deshalb selbst keine Warnungen aus", "the custom rule"},
	{"dataguard.empty_events", "Sie erscheinen hier", "the events"},
	{"registrations.empty_all", "Sie erscheinen hier", "the join requests"},
	{"help_request.empty_all", "Sie erscheinen hier", "the help requests"},
	{"organizations.invite_invalid", "Sie ist vielleicht abgelaufen", "the invitation"},
	{"organizations.org_not_found", "Sie wurde vielleicht gelöscht", "the organisation"},
	{"fleet.no_compliance_data", "Sie erscheinen, nachdem", "the compliance results"},
	{"resource_control.disks_hidden_hint_new", "Sie sind für Personen am Gerät nicht sichtbar", "the drives"},
	{"server_mgmt.files_roots_none", "Sie beginnt in /", "the console"},
	{"setup_guide.steps.backup.hint", "Sie enthält den Schlüssel des Servers", "the backup"},
	{"agents.what_body", "Sie registrieren sich am Server", "the support agents"},
	{"agents.what_body", "Sie starten keine ausgehenden Verbindungen", "the support agents"},
	{"agents.page_description", "Sie nimmt Fernhilfe an", "the app"},
	{"onboard.support.text", "Sie lädt eine kleine App herunter", "the person who needs help"},
	{"onboard.public.support_steps_macos_1", "Sie läuft aus deinem Download-Ordner", "the downloaded file"},
	{"remote_viewer.open_in_new_tab_hint", "Sie verbindet sich dort neu", "the session"},
	{"email_change.notice_lead", "Sie ändert sich, sobald", "the address"},
	{"settings.notices.note", "Sie gehen nur raus", "the mails"},
	{"users.bulk_delete_effect", "Ihre API-Token werden widerrufen", "the accounts"},
	{"users.bulk_disable_effect", "Ihre Sitzungen und Zugriffstoken enden", "the accounts"},
	{"users.bulk_password_change_effect", "Ihre Sitzungen laufen bis dahin weiter", "the accounts"},
	{"users.bulk_role_effect", "Ihre Sitzungen und Zugriffstoken enden", "the accounts"},
	{"users.delete_handover_hint_no_orgs", "Sie zu übergeben", "the memberships"},
	{"users.import_none_next", "Sie können sich noch nicht anmelden", "the imported accounts"},
}

// germanTexts reads the console's German language file as key to text.
func germanTexts(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(realLangDir, "de.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, inner := range x {
				walk(prefix+k+".", inner)
			}
		case string:
			texts[strings.TrimSuffix(prefix, ".")] = x
		}
	}
	walk("", tree)
	return texts
}

// The German texts of the console and of the mails the server sends say
// du, like the desktop client and the website (BUGS.md L303). A Sie that
// means they is listed in theyNotYou; an entry whose words are gone or
// hold no Sie any more fails, so the list stays true.
func TestGermanSaysDu(t *testing.T) {
	texts := germanTexts(t)
	allowed := map[string][]string{}
	for _, a := range theyNotYou {
		text, ok := texts[a.key]
		switch {
		case !ok:
			t.Errorf("listed %s is not in de.json", a.key)
		case !strings.Contains(text, a.words):
			t.Errorf("listed %s no longer has %q: %q", a.key, a.words, text)
		case !formalAddress.MatchString(a.words):
			t.Errorf("listed words of %s hold no Sie: %q", a.key, a.words)
		}
		allowed[a.key] = append(allowed[a.key], a.words)
	}
	var found []string
	for key, text := range texts {
		for _, words := range allowed[key] {
			text = strings.ReplaceAll(text, words, "")
		}
		if m := formalAddress.FindStringSubmatch(text); m != nil {
			found = append(found, key+" ("+m[1]+"): "+texts[key])
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("%d German texts address the reader with Sie:\n%s", len(found), strings.Join(found, "\n"))
	}
}
