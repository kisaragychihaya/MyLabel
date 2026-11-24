package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/app"
	"mylabel/locales"
)

// TestFirstFrameBuilds is the smoke test of the app's first frame under
// ui.Tester: app.New() must build a view that shows the toolbar's
// open-folder button and the (empty) category panel — a new workspace starts
// with no categories, so the panel shows its hint. The texts are looked up
// rather than spelled out, so the test holds in whatever language the run
// shows (MYGO_LOCALE).
func TestFirstFrameBuilds(t *testing.T) {
	a := app.New()
	tt := ui.NewTester(a.View, 1280, 800)
	if open := locales.ToolbarOpenFolder(); !tt.HasText(open) {
		t.Fatalf("first frame shows no open-folder button (%q); texts = %v", open, tt.Texts())
	}
	if hint := locales.CatPanelEmpty(); !tt.HasText(hint) {
		t.Errorf("first frame shows no empty-category hint (%q); texts = %v", hint, tt.Texts())
	}
	if tt.HasText("cat1") {
		t.Errorf("a new workspace must not start with default categories; texts = %v", tt.Texts())
	}
	if add, del := locales.CommonAdd(), locales.CommonDelete(); !tt.HasText(add) || !tt.HasText(del) {
		t.Errorf("first frame shows no category panel buttons (%q, %q); texts = %v", add, del, tt.Texts())
	}
}
