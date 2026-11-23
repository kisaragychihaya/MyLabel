package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/app"
)

// TestFirstFrameBuilds is the smoke test of the app's first frame under
// ui.Tester: app.New() must build a view that shows the toolbar's
// open-folder button and the (empty) category panel — a new workspace starts
// with no categories, so the panel shows its hint.
func TestFirstFrameBuilds(t *testing.T) {
	a := app.New()
	tt := ui.NewTester(a.View, 1280, 800)
	if !tt.HasText("打开文件夹") {
		t.Fatalf("first frame shows no open-folder button; texts = %v", tt.Texts())
	}
	if !tt.HasText("暂无类别") {
		t.Errorf("first frame shows no empty-category hint; texts = %v", tt.Texts())
	}
	if tt.HasText("cat1") {
		t.Errorf("a new workspace must not start with default categories; texts = %v", tt.Texts())
	}
	if !tt.HasText("添加") || !tt.HasText("删除") {
		t.Errorf("first frame shows no category panel buttons; texts = %v", tt.Texts())
	}
}
