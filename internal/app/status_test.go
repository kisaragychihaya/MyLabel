package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/store"
)

// TestNormalizeStatus checks that a row without a status reads as new, which is
// what the store writes for a fresh image.
func TestNormalizeStatus(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", store.StatusNew},
		{store.StatusNew, store.StatusNew},
		{store.StatusLabeling, store.StatusLabeling},
		{store.StatusDone, store.StatusDone},
		{store.StatusReviewed, store.StatusReviewed},
		{store.StatusSkipped, store.StatusSkipped},
		{"weird", "weird"},
	} {
		if got := normalizeStatus(tc.in); got != tc.want {
			t.Errorf("normalizeStatus(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestStatusDotColor is the badge table of §7: labeling blue, done green,
// reviewed purple, skipped gray, and no badge for a fresh image.
func TestStatusDotColor(t *testing.T) {
	light := ui.LightTheme()
	for _, tc := range []struct {
		status string
		want   ui.Color
		ok     bool
	}{
		{store.StatusLabeling, light.Accent, true},
		{store.StatusDone, light.Success, true},
		{store.StatusReviewed, ui.RGB(0x8b, 0x5c, 0xf6), true},
		{store.StatusSkipped, light.TextMuted, true},
		{store.StatusNew, ui.Color{}, false},
		{"", ui.Color{}, false},
	} {
		got, ok := statusDotColor(tc.status, light)
		if ok != tc.ok {
			t.Errorf("statusDotColor(%q) ok = %v，期望 %v", tc.status, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("statusDotColor(%q) = %v，期望 %v", tc.status, got, tc.want)
		}
	}
}

// TestToggleImageStatusWritesRow is §7 item 2: Ctrl+D's action flips the loaded
// image between done and new, writes that row at once, and does not leave the
// document unsaved.
func TestToggleImageStatusWritesRow(t *testing.T) {
	a := storeApp(t, "a.png")
	if got := normalizeStatus(a.images[0].Status); got != store.StatusNew {
		t.Fatalf("新图片状态 = %q，期望 new", got)
	}
	a.dirty = false
	a.dirtyImages = make(map[int64]bool)

	a.toggleImageStatus()
	if got := a.images[0].Status; got != store.StatusDone {
		t.Errorf("切换后状态 = %q，期望 done", got)
	}
	if a.dirty {
		t.Error("状态切换不应把文档标为未保存")
	}
	if len(a.dirtyImages) != 0 {
		t.Errorf("状态切换不应进入 dirtyImages: %v", a.dirtyImages)
	}
	if status := storedStatus(t, a); status != store.StatusDone {
		t.Errorf("库中状态 = %q，期望 done（即时写库）", status)
	}

	a.toggleImageStatus()
	if got := a.images[0].Status; got != store.StatusNew {
		t.Errorf("再次切换后状态 = %q，期望 new", got)
	}
	if status := storedStatus(t, a); status != store.StatusNew {
		t.Errorf("库中状态 = %q，期望 new", status)
	}
}

// TestToggleFromOtherStatusReachesDone checks that a status outside the pair
// still reaches it with one press: reviewed or labeling become done.
func TestToggleFromOtherStatusReachesDone(t *testing.T) {
	a := New()
	a.images = []ImageInfo{{Path: "t1", Name: "t1.png", Width: 8, Height: 8, Status: store.StatusReviewed}}
	a.loaded = 0
	a.toggleImageStatus()
	if got := a.images[0].Status; got != store.StatusDone {
		t.Errorf("reviewed 上按 Ctrl+D 后状态 = %q，期望 done", got)
	}
}

// TestFirstEditUpgradesToLabeling is §7 item 2: the first annotation change of a
// fresh image upgrades it to labeling, and writes the row right away.
func TestFirstEditUpgradesToLabeling(t *testing.T) {
	a := storeApp(t, "a.png")
	a.push(cmdCreate(a, 0, Ann{ID: "ann_x", Cat: 0, CX: 5, CY: 5, W: 10, H: 10}))

	if got := a.images[0].Status; got != store.StatusLabeling {
		t.Errorf("首次编辑后状态 = %q，期望 labeling", got)
	}
	if status := storedStatus(t, a); status != store.StatusLabeling {
		t.Errorf("库中状态 = %q，期望 labeling", status)
	}
	if !a.dirty {
		t.Error("标注改动仍应把文档标为未保存")
	}
}

// TestEditKeepsUserSetStatus checks that labeling only upgrades a fresh image:
// a status the user chose stays.
func TestEditKeepsUserSetStatus(t *testing.T) {
	a := storeApp(t, "a.png")
	a.writeImageStatus(0, store.StatusDone)
	a.push(cmdCreate(a, 0, Ann{ID: "ann_x", Cat: 0, CX: 5, CY: 5, W: 10, H: 10}))
	if got := a.images[0].Status; got != store.StatusDone {
		t.Errorf("编辑后状态 = %q，期望保持 done", got)
	}
}

// TestThumbnailStatusDot is §8 item 5's badge: a done image shows a green dot
// in the corner of its thumbnail cell, a fresh one shows none.
func TestThumbnailStatusDot(t *testing.T) {
	a := newTestApp()
	a.images[0].Status = store.StatusDone
	a.thumbs["t1"] = testImage(8, 8)
	tt := panelFor(t, a)
	tt.Frame()

	dot, ok := tt.Find("done")
	if !ok {
		t.Fatalf("已完成的图片没有圆点；texts = %v", tt.Texts())
	}
	if dot.W < 6 || dot.H < 6 {
		t.Errorf("圆点尺寸 = %v×%v，期望 8×8", dot.W, dot.H)
	}
	if _, ok := tt.Find("new"); ok {
		t.Error("新图片不应有圆点")
	}

	img := tt.Image()
	px := img.RGBAAt(int(dot.X+dot.W/2), int(dot.Y+dot.H/2))
	if !(px.G > px.R && px.G > px.B && px.G > 0x60) {
		t.Errorf("圆点像素 = %v，期望主题的成功色（绿）", px)
	}
}

// TestCtrlDTogglesStatus is the key binding of §7: App.View maps Ctrl+D to the
// toggle, so the real view is what this checks.
func TestCtrlDTogglesStatus(t *testing.T) {
	a, tt := realViewTester(t)
	if a.loaded < 0 {
		t.Fatal("真实视图没有载入图片")
	}
	was := normalizeStatus(a.images[a.loaded].Status)

	tt.Key(ui.Cmd, ui.KeyD)
	tt.Frame()
	if got := normalizeStatus(a.images[a.loaded].Status); got != store.StatusDone {
		t.Fatalf("Ctrl+D 后状态 = %q，期望 done（原为 %q）", got, was)
	}
	tt.Key(ui.Cmd, ui.KeyD)
	tt.Frame()
	if got := normalizeStatus(a.images[a.loaded].Status); got != store.StatusNew {
		t.Errorf("再次 Ctrl+D 后状态 = %q，期望 new", got)
	}
}

// storedStatus reads the first image's status back from the repository, which
// is what a restart would see.
func storedStatus(t *testing.T, a *App) string {
	t.Helper()
	recs, err := a.store.LoadImages()
	if err != nil {
		t.Fatalf("LoadImages: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("库中没有图片")
	}
	return normalizeStatus(recs[0].Status)
}
