package onboard

import "testing"

// The quick support mail and the invite's lines are in the reader's
// language: no line of it is the English text in another language.
func TestTheMailIsTranslated(t *testing.T) {
	en := inLanguage(t, "en")
	for _, lang := range []string{"ar", "cs", "da", "de", "es", "fi", "fr", "hi", "hu", "id", "it", "ja", "ko", "nb", "nl", "pl", "pt", "ro", "sv", "th", "tr", "uk", "vi", "zh", "zh-TW"} {
		tr := inLanguage(t, lang)
		for _, key := range []string{"support_subject", "support_heading", "support_intro", "support_steps", "support_button", "support_expires", "invite_expires", "invite_code", "footer"} {
			if got := tr("onboard.mail."+key, nil); got == en("onboard.mail."+key, nil) {
				t.Errorf("%s: onboard.mail.%s is the English text %q", lang, key, got)
			}
		}
	}
}
