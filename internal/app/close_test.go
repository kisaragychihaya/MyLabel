package app

import (
	"reflect"
	"testing"
)

// TestConfirmCloseWithoutWindow covers the no-window path: no dialog can be
// shown, so the save result alone decides whether the window may close.
func TestConfirmCloseWithoutWindow(t *testing.T) {
	// Nothing to save.
	if a := New(); !a.ConfirmClose() {
		t.Error("无窗口、无修改时 ConfirmClose 应返回 true")
	}

	// A working save passes and the annotations reach the store.
	b := storeApp(t, "a.png")
	b.images[0].Anns = testAnns
	b.setDirty(0)
	if !b.ConfirmClose() {
		t.Error("保存成功时 ConfirmClose 应返回 true")
	}
	if b.dirty {
		t.Error("ConfirmClose 保存后 dirty 应为 false")
	}
	recs, err := b.store.LoadImages()
	if err != nil {
		t.Fatalf("LoadImages: %v", err)
	}
	if len(recs) != 1 || !reflect.DeepEqual(annsFromRecs(recs[0].Anns), testAnns) {
		t.Errorf("库中标注 = %+v，期望 %+v", recs, testAnns)
	}

	// A failing save keeps the window open: without a dialog there is no way
	// to answer "discard the changes?".
	c := storeApp(t, "a.png")
	c.images[0].Anns = testAnns
	c.setDirty(0)
	_ = c.store.Close()
	if c.ConfirmClose() {
		t.Error("保存失败且无窗口时 ConfirmClose 应返回 false")
	}
	if !c.dirty {
		t.Error("保存失败后应保持 dirty")
	}
}
