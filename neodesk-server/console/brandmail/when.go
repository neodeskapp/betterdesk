package brandmail

import (
	"strconv"
	"strings"
	"time"
)

// dateStyle is how a language writes a date: its month names (in the
// form a date takes, the genitive in Polish, Czech and Ukrainian), the
// date with {d}, {m} (the month's name or number), {y}, and the date with
// a time, {date}, {time} and {zone}. sep is the separator between hours
// and minutes.
type dateStyle struct {
	months   [12]string
	date     string
	dateTime string
	sep      string
}

var numbers = [12]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}

var dateStyles = map[string]dateStyle{
	"en": {months: [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
		date: "{d} {m} {y}", dateTime: "{date}, {time} {zone}"},
	"de": {months: [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"},
		date: "{d}. {m} {y}", dateTime: "{date}, {time} Uhr {zone}"},
	"fr": {months: [12]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
		date: "{d} {m} {y}", dateTime: "{date} à {time} {zone}"},
	"es": {months: [12]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
		date: "{d} de {m} de {y}", dateTime: "{date}, {time} {zone}"},
	"it": {months: [12]string{"gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno", "luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre"},
		date: "{d} {m} {y}", dateTime: "{date}, {time} {zone}"},
	"pt": {months: [12]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"},
		date: "{d} de {m} de {y}", dateTime: "{date}, {time} {zone}"},
	"nl": {months: [12]string{"januari", "februari", "maart", "april", "mei", "juni", "juli", "augustus", "september", "oktober", "november", "december"},
		date: "{d} {m} {y}", dateTime: "{date}, {time} {zone}"},
	"pl": {months: [12]string{"stycznia", "lutego", "marca", "kwietnia", "maja", "czerwca", "lipca", "sierpnia", "września", "października", "listopada", "grudnia"},
		date: "{d} {m} {y}", dateTime: "{date}, {time} {zone}"},
	"cs": {months: [12]string{"ledna", "února", "března", "dubna", "května", "června", "července", "srpna", "září", "října", "listopadu", "prosince"},
		date: "{d}. {m} {y}", dateTime: "{date}, {time} {zone}"},
	"da": {months: [12]string{"januar", "februar", "marts", "april", "maj", "juni", "juli", "august", "september", "oktober", "november", "december"},
		date: "{d}. {m} {y}", dateTime: "{date} kl. {time} {zone}", sep: "."},
	"nb": {months: [12]string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"},
		date: "{d}. {m} {y}", dateTime: "{date} kl. {time} {zone}"},
	"sv": {months: [12]string{"januari", "februari", "mars", "april", "maj", "juni", "juli", "augusti", "september", "oktober", "november", "december"},
		date: "{d} {m} {y}", dateTime: "{date} kl. {time} {zone}"},
	"fi": {months: [12]string{"tammikuuta", "helmikuuta", "maaliskuuta", "huhtikuuta", "toukokuuta", "kesäkuuta", "heinäkuuta", "elokuuta", "syyskuuta", "lokakuuta", "marraskuuta", "joulukuuta"},
		date: "{d}. {m} {y}", dateTime: "{date} klo {time} {zone}", sep: "."},
	"hu": {months: [12]string{"január", "február", "március", "április", "május", "június", "július", "augusztus", "szeptember", "október", "november", "december"},
		date: "{y}. {m} {d}.", dateTime: "{date} {time} {zone}"},
	"ro": {months: [12]string{"ianuarie", "februarie", "martie", "aprilie", "mai", "iunie", "iulie", "august", "septembrie", "octombrie", "noiembrie", "decembrie"},
		date: "{d} {m} {y}", dateTime: "{date}, {time} {zone}"},
	"tr": {months: [12]string{"Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"},
		date: "{d} {m} {y}", dateTime: "{date} {time} {zone}"},
	"uk": {months: [12]string{"січня", "лютого", "березня", "квітня", "травня", "червня", "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"},
		date: "{d} {m} {y} р.", dateTime: "{date}, {time} {zone}"},
	"ar": {months: [12]string{"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو", "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"},
		date: "{d} {m} {y}", dateTime: "{date}، {time} {zone}"},
	"hi": {months: [12]string{"जनवरी", "फ़रवरी", "मार्च", "अप्रैल", "मई", "जून", "जुलाई", "अगस्त", "सितंबर", "अक्तूबर", "नवंबर", "दिसंबर"},
		date: "{d} {m} {y}", dateTime: "{date}, {time} {zone}"},
	"id": {months: [12]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"},
		date: "{d} {m} {y}", dateTime: "{date} pukul {time} {zone}", sep: "."},
	"th": {months: [12]string{"มกราคม", "กุมภาพันธ์", "มีนาคม", "เมษายน", "พฤษภาคม", "มิถุนายน", "กรกฎาคม", "สิงหาคม", "กันยายน", "ตุลาคม", "พฤศจิกายน", "ธันวาคม"},
		date: "{d} {m} {y}", dateTime: "{date} เวลา {time} {zone}"},
	"vi":    {months: numbers, date: "ngày {d} tháng {m} năm {y}", dateTime: "{time} {zone}, {date}"},
	"ja":    {months: numbers, date: "{y}年{m}月{d}日", dateTime: "{date} {time} {zone}"},
	"ko":    {months: numbers, date: "{y}년 {m}월 {d}일", dateTime: "{date} {time} {zone}"},
	"zh":    {months: numbers, date: "{y}年{m}月{d}日", dateTime: "{date} {time} {zone}"},
	"zh-TW": {months: numbers, date: "{y}年{m}月{d}日", dateTime: "{date} {time} {zone}"},
}

// styleOf is the date style of a language code ("de", "pt-BR"), English
// for a language without one.
func styleOf(lang string) dateStyle {
	if s, ok := dateStyles[lang]; ok {
		return s
	}
	if base, _, ok := strings.Cut(lang, "-"); ok {
		if s, ok := dateStyles[base]; ok {
			return s
		}
	}
	return dateStyles["en"]
}

// Zone is where a reader's times are said: the account's time zone (an
// IANA name such as "Europe/Berlin") when it has one that loads, else
// the server's own (time.Local).
func Zone(name string) *time.Location {
	if name = strings.TrimSpace(name); name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.Local
}

// zoneLabel is the name of the zone at t: its abbreviation ("CET",
// "UTC"), or the offset from UTC ("UTC+3", "UTC-3:30") where the zone has
// no abbreviation.
func zoneLabel(t time.Time) string {
	name, offset := t.Zone()
	if name != "" && name[0] != '+' && name[0] != '-' && name != "Local" {
		return name
	}
	if offset == 0 {
		return "UTC"
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	label := "UTC" + sign + strconv.Itoa(offset/3600)
	if m := offset % 3600 / 60; m != 0 {
		label += ":" + twoDigits(m)
	}
	return label
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func fill(pattern string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		pattern = strings.ReplaceAll(pattern, "{"+pairs[i]+"}", pairs[i+1])
	}
	return pattern
}

// Date is the day of t in loc, written the way lang writes a date:
// "3 December 2026", "3. Dezember 2026".
func Date(t time.Time, lang string, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	t = t.In(loc)
	s := styleOf(lang)
	return fill(s.date, "d", strconv.Itoa(t.Day()), "m", s.months[t.Month()-1], "y", strconv.Itoa(t.Year()))
}

// DateTime is t in loc with the day, the time on a 24 hour clock and the
// zone's name, the way lang writes them: "3 December 2026, 01:12 CET",
// "3. Dezember 2026, 01:12 Uhr CET". A mail never shows a bare UTC
// timestamp; the zone is the reader's (Zone).
func DateTime(t time.Time, lang string, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	t = t.In(loc)
	s := styleOf(lang)
	sep := s.sep
	if sep == "" {
		sep = ":"
	}
	clock := twoDigits(t.Hour()) + sep + twoDigits(t.Minute())
	return fill(s.dateTime, "date", Date(t, lang, loc), "time", clock, "zone", zoneLabel(t))
}
