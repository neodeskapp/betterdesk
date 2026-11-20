package i18n

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A bundle on FS answers as one on the same files in Dir: the languages,
// their texts, the load messages and the translations page, odd files
// included. Only writes differ: in FS, the languages built into the
// server, they are refused and change nothing (BUGS NL1).
func TestBundleOnFSReadsAsOnDisk(t *testing.T) {
	dir := copyDir(t, realLangDir)
	for name, content := range map[string]string{
		"xx.json":     `{"_meta":{"code":"xx","name":"Unknown"},"nav":{"devices":"x"}}`,
		"broken.json": `{"_meta":`,
		"sv.json":     byteOrderMark + `{"_meta":{"code":"sv","name":"Swedish"},"nav":{"devices":"Enheter"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	onDisk, diskLogs := loaded(t, dir, "en")
	fsLogs := &logs{}
	// Dir only names the folder in messages; nothing is read from it
	inFS := New(filepath.Join(t.TempDir(), "named"), "en")
	inFS.FS = os.DirFS(dir)
	inFS.Logf = fsLogs.logf
	inFS.Load()
	if got, want := fsLogs.take(), diskLogs.take(); !slices.Equal(got, want) {
		t.Errorf("load messages from FS:\n%q\nfrom disk:\n%q", got, want)
	}
	if len(inFS.order) < 20 {
		t.Fatalf("only %d languages from FS", len(inFS.order))
	}

	same := func(what string, fromFS, fromDisk any) {
		t.Helper()
		if got, want := stringify(fromFS, ""), stringify(fromDisk, ""); got != want {
			t.Errorf("%s from FS differs from disk:\n%.300s\n%.300s", what, got, want)
		}
	}
	same("Available", inFS.Available(), onDisk.Available())
	listFS, errFS := inFS.AvailableList()
	listDisk, errDisk := onDisk.AvailableList()
	if errFS != nil || errDisk != nil {
		t.Fatal(errFS, errDisk)
	}
	same("AvailableList", listFS, listDisk)
	for _, l := range append(knownLanguages, knownLanguage{code: "xx"}) {
		same("Translations "+l.code, inFS.Translations(l.code), onDisk.Translations(l.code))
		same("Merged "+l.code, inFS.Merged(l.code), onDisk.Merged(l.code))
	}

	hFS, hDisk := &Handlers{Bundle: inFS}, &Handlers{Bundle: onDisk}
	pagesFS, totalFS, errFS := hFS.panelLanguages()
	pagesDisk, totalDisk, errDisk := hDisk.panelLanguages()
	if errFS != nil || errDisk != nil || totalFS != totalDisk || totalFS == 0 {
		t.Fatalf("panel languages: %v %v, totals %d %d", errFS, errDisk, totalFS, totalDisk)
	}
	same("the translations page", pagesFS, pagesDisk)
	for _, code := range []string{"de", "xx", "broken", "sv", "nope", "d_e", "zh-TW"} {
		gotFS, statusFS := hFS.panelMissing(code)
		gotDisk, statusDisk := hDisk.panelMissing(code)
		if statusFS != statusDisk || (gotFS == nil) != (gotDisk == nil) {
			t.Errorf("missing keys of %s: status %d from FS, %d from disk", code, statusFS, statusDisk)
		}
		if gotFS != nil && gotDisk != nil {
			same("missing keys of "+code, gotFS, gotDisk)
		}
	}
	if _, status := hFS.panelMissing("nope"); status != http.StatusNotFound {
		t.Errorf("missing keys of a language without a file in FS: %d", status)
	}

	// Writes change nothing in FS
	file := filepath.Join(dir, "de.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	texts := stringify(inFS.Merged("de"), "")
	if r := inFS.Save("de", NewObject()); r.Success || !r.BuiltIn || r.Error != "Languages ship with the server and cannot be uploaded" {
		t.Errorf("Save in FS: %+v", r)
	}
	if r := inFS.Delete("de"); r.Success || !r.BuiltIn || r.Error != "Languages ship with the server and cannot be deleted" {
		t.Errorf("Delete in FS: %+v", r)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Error("a write in FS changed the file on disk")
	}
	if !inFS.Has("de") || stringify(inFS.Merged("de"), "") != texts {
		t.Error("a failed write in FS changed the loaded language")
	}
	// A language without a file is not found, as on disk
	if r := inFS.Delete("yy"); r.Success || !r.NotFound {
		t.Errorf("Delete of a language without a file in FS: %+v", r)
	}
	// On disk the same writes go through
	if r := onDisk.Delete("de"); !r.Success || onDisk.Has("de") {
		t.Errorf("Delete on disk: %+v", r)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("de.json after a delete on disk: %v", err)
	}
}

// Without the folder in FS the bundle has no languages and says so, as
// without the folder on disk.
func TestBundleOnFSWithoutTheFolder(t *testing.T) {
	l := &logs{}
	b := New("embedded/lang", "en")
	b.FS = os.DirFS(filepath.Join(t.TempDir(), "missing"))
	b.Logf = l.logf
	b.Load()
	if got := l.take(); !slices.Equal(got, []string{"Language directory not found embedded/lang", "i18n: Loaded languages 0"}) {
		t.Errorf("messages: %q", got)
	}
}
