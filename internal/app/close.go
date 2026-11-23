package app

import "github.com/egoist/mygo"

// ConfirmClose flushes pending changes and asks the user before closing the
// window with unsaved changes. It returns true when the window may close.
//
// Saving happens first, so the normal path never shows a dialog: only a failed
// save leaves the document dirty. Without a window (unit tests) no dialog can
// be shown, so the save result alone decides.
func (a *App) ConfirmClose() bool {
	err := a.save()
	if a.win == nil {
		return err == nil
	}
	if !a.dirty {
		return true
	}

	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:             a.win,
		Type:               mygo.MessageWarning,
		Title:              "未保存的修改",
		Message:            "标注尚未保存，退出前是否保存？",
		Buttons:            []string{"保存并退出", "不保存退出", "取消"},
		DefaultButton:      0,
		CancelButton:       2,
		DestructiveButtons: []int{1},
	})
	if err != nil {
		return false
	}
	switch res.Button {
	case 0:
		return a.save() == nil
	case 1:
		return true
	default:
		return false
	}
}
