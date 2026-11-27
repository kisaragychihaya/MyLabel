package app

import (
	"os"
	"testing"

	"mylabel/locales"
)

// TestMain pins the language of the tests: every assertion on the interface's
// text is written in the source language, and MYGO_LOCALE is what mygo reads
// when it resolves the app's language. The texts themselves are looked up per
// call, so a test that checks another language passes locales.In itself.
func TestMain(m *testing.M) {
	os.Setenv("MYGO_LOCALE", "zh-Hans")
	// The AI runtime bootstrap hits the network when Detect fails under the
	// test binary (the window-less path runs it synchronously); tests inject
	// their own client and never want that.
	os.Setenv("MYLABEL_NO_SAM_BOOTSTRAP", "1")
	os.Exit(m.Run())
}

// TestLocalesEnglishSmoke: the en catalog is complete enough to show the
// interface in English — its messages are non-empty and say something else
// than the source language.
func TestLocalesEnglishSmoke(t *testing.T) {
	en := locales.In("en")
	cases := []struct {
		name       string
		en, source string
	}{
		{"StatusFirstImage", en.StatusFirstImage(), locales.StatusFirstImage()},
		{"ToolbarOpenFolder", en.ToolbarOpenFolder(), locales.ToolbarOpenFolder()},
		{"CommonCancel", en.CommonCancel(), locales.CommonCancel()},
		{"CatPanelTitle", en.CatPanelTitle(), locales.CatPanelTitle()},
		{"WizardExportStart", en.WizardExportStart(), locales.WizardExportStart()},
	}
	for _, c := range cases {
		if c.en == "" {
			t.Errorf("%s: the en message is empty", c.name)
		}
		if c.en == c.source {
			t.Errorf("%s: en = %q, the same as the source language", c.name, c.en)
		}
	}
}
