package app

import (
	"github.com/egoist/mygo/ui"

	"mylabel/internal/store"
)

// normalizeStatus maps a stored status to the one the UI works with: a row
// that never had one is a fresh image (docs/phase2-design.md §7).
func normalizeStatus(status string) string {
	if status == "" {
		return store.StatusNew
	}
	return status
}

// statusLabel is the status bar's name for a status, in the interface's
// language.
func statusLabel(status string) string {
	switch normalizeStatus(status) {
	case store.StatusNew:
		return "新图"
	case store.StatusLabeling:
		return "标注中"
	case store.StatusDone:
		return "已完成"
	case store.StatusReviewed:
		return "已复核"
	case store.StatusSkipped:
		return "已跳过"
	}
	return normalizeStatus(status)
}

// statusDotColor returns the thumbnail badge color of a status and whether it
// has a badge at all: a fresh image shows none (§7).
func statusDotColor(status string, t *ui.Theme) (ui.Color, bool) {
	switch normalizeStatus(status) {
	case store.StatusLabeling:
		return t.Accent, true
	case store.StatusDone:
		return t.Success, true
	case store.StatusReviewed:
		return ui.RGB(0x8b, 0x5c, 0xf6), true
	case store.StatusSkipped:
		return t.TextMuted, true
	}
	return ui.Color{}, false
}

// writeImageStatus sets one image's status and writes that single row right
// away: a status change is not part of the document's unsaved annotation state,
// so it neither waits for the debounced save nor marks anything dirty (§7). A
// failed write puts the old status back, so screen and database stay equal.
func (a *App) writeImageStatus(img int, status string) {
	if img < 0 || img >= len(a.images) {
		return
	}
	info := &a.images[img]
	if normalizeStatus(info.Status) == status {
		return
	}
	prev := info.Status
	info.Status = status
	if a.store == nil || info.ID == 0 {
		return // no row to write: unit tests and unsaved images
	}
	if err := a.store.SetImageStatus(info.ID, status); err != nil {
		info.Status = prev
		a.lastStatus = "写入状态失败: " + err.Error()
		return
	}
	a.lastStatus = "状态: " + statusLabel(status)
}

// toggleImageStatus flips the loaded image between done and new (Ctrl+D, §7).
// Any other status becomes done, so one key press always reaches that pair.
func (a *App) toggleImageStatus() {
	img := a.loaded
	if img < 0 || img >= len(a.images) {
		return
	}
	if normalizeStatus(a.images[img].Status) == store.StatusDone {
		a.writeImageStatus(img, store.StatusNew)
		return
	}
	a.writeImageStatus(img, store.StatusDone)
}

// markLabeling upgrades a fresh image to labeling the first time its
// annotations change (§7). A status the user set is left alone.
func (a *App) markLabeling(img int) {
	if img < 0 {
		img = a.loaded
	}
	if img < 0 || img >= len(a.images) {
		return
	}
	if normalizeStatus(a.images[img].Status) != store.StatusNew {
		return
	}
	a.writeImageStatus(img, store.StatusLabeling)
}
