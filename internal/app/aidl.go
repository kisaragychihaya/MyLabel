package app

// First-run acquisition of the AI assist runtime (docs/phase7-design.md §4).
// The strip's AI button is always there; activating it without a runtime
// opens the consent modal, and confirming starts the model download with a
// progress modal both can cancel. The native DLL itself is the only piece
// downloaded automatically (a fire-and-forget goroutine at startup,
// internal/sam2.EnsureDLL); the ~106 MB model set moves only on the user's
// word, through sam2.DownloadModel.
//
// The state machine lives on the main thread: aiBootState renders the modal,
// aiBootGen invalidates a download whose cancel/restart made its result
// stale, and aiDL holds what the progress modal shows.

import (
	"context"
	"errors"
	"os"

	"github.com/egoist/mygo/ui"

	"mylabel/internal/sam2"
	"mylabel/locales"
)

// aiBootState is where the runtime acquisition stands; only aiBootAsk and
// aiBootModel render the modal, aiBootNone is every other moment.
const (
	aiBootNone = iota
	aiBootAsk  // consent modal: the user decides whether the model may download
	aiBootModel
)

// aiDLUI is what the progress modal shows: the file in flight, its bytes
// against its size, and how many of the set's files finished.
type aiDLUI struct {
	name        string
	bytes, total int64
	done, count int
}

func (u *aiDLUI) set(p sam2.ModelProgress) {
	u.name, u.bytes, u.total = p.Name, p.Bytes, p.Total
	u.done, u.count = p.Done, p.Count
}

// aiAskBootstrap is what pressing the AI tool without a runtime does: the
// consent modal opens (or, already downloading, the progress modal is what
// the user gets — the state renders it anyway).
func (a *App) aiAskBootstrap() {
	switch a.aiBootState {
	case aiBootAsk, aiBootModel:
	default:
		a.aiBootState = aiBootAsk
	}
}

// aiStartModelDownload moves the consent to the download: it cancels nothing
// (there is nothing yet), arms the cancel func and the generation, and runs
// sam2.DownloadModel — or the test-injected stand-in — off the main thread
// when a window exists.
func (a *App) aiStartModelDownload() {
	dl := a.aiDownloadModel
	if dl == nil {
		dl = func(ctx context.Context, dir string, cb func(sam2.ModelProgress)) error {
			return sam2.DownloadModel(ctx, dir, cb)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.aiDLCancel = cancel
	a.aiBootGen++
	gen := a.aiBootGen
	a.aiBootState = aiBootModel
	dir := a.aiBootDir
	win := a.win
	if win == nil {
		// No window (tests, headless): run inline, progress lands directly.
		a.aiModelLanded(gen, dl(ctx, dir, func(p sam2.ModelProgress) { a.aiDL.set(p) }))
		return
	}
	go func() {
		err := dl(ctx, dir, func(p sam2.ModelProgress) {
			win.Update(func() { a.aiDL.set(p) })
		})
		win.Update(func() { a.aiModelLanded(gen, err) })
	}()
}

// aiCancelModelDownload is the progress modal's cancel: the context aborts
// the transfer, the modal closes via the state, and the worker's late result
// is discarded by the generation check.
func (a *App) aiCancelModelDownload() {
	a.aiBootGen++
	if a.aiDLCancel != nil {
		a.aiDLCancel()
	}
	a.aiDLCancel = nil
	a.aiBootState = aiBootNone
}

// aiModelLanded runs on the main thread with the download's outcome: success
// creates the client (the AI tool becomes active on the next press), a
// cancellation or a failure only says so in the status bar — the modal is
// gone either way and the next press of the button starts over.
func (a *App) aiModelLanded(gen uint64, err error) {
	if gen != a.aiBootGen {
		return // a cancel or a newer download superseded this result
	}
	a.aiDLCancel = nil
	a.aiBootState = aiBootNone
	switch {
	case err == nil:
		cfg, ok := sam2.Detect(a.aiBootDir)
		if !ok {
			a.lastStatus = locales.StatusAIBootstrapFailed(locales.AiDownloadIncomplete())
			return
		}
		a.sam = sam2.New(cfg)
		a.lastStatus = locales.StatusAIBootstrapDone()
	case errors.Is(err, context.Canceled):
		a.lastStatus = locales.StatusAIBootstrapCancelled()
	default:
		a.lastStatus = locales.StatusAIBootstrapFailed(err.Error())
	}
}

// aiDownloadModal renders the consent or the progress modal, whichever the
// state says. ui.Modal owns `open`: Esc or the backdrop sets it false, which
// is a cancel in both states — the same outcome as the explicit button.
func (a *App) aiDownloadModal(c *ui.Context) {
	var title string
	switch a.aiBootState {
	case aiBootAsk:
		title = locales.AiDownloadTitle()
	case aiBootModel:
		title = locales.AiDownloadProgressTitle()
	default:
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		t := c.Theme()
		ui.Column(c).Width(380).Gap(10).Padding(4).Children(func() {
			ui.Text(c, title).FontSize(14).Bold()
			if a.aiBootState == aiBootAsk {
				ui.Text(c, locales.AiDownloadMessage()).FontSize(12).TextColor(t.Text)
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c.Key("aiDlStart"), locales.AiDownloadStart()).Clicked() {
						a.aiStartModelDownload()
					}
					if ui.Button(c.Key("aiDlCancel"), locales.CommonCancel()).Clicked() {
						open = false
					}
				})
				return
			}
			pct := int64(0)
			if a.aiDL.total > 0 {
				pct = a.aiDL.bytes * 100 / a.aiDL.total
			}
			ui.Text(c, locales.AiDownloadProgress(a.aiDL.name, int(pct))).FontSize(12).TextColor(t.Text)
			ui.Text(c, locales.AiDownloadOverall(a.aiDL.done, a.aiDL.count)).FontSize(11).TextColor(t.TextMuted)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c.Key("aiDlCancel"), locales.CommonCancel()).Clicked() {
					open = false
				}
			})
		})
	})
	if !open {
		if a.aiBootState == aiBootModel {
			a.aiCancelModelDownload()
		} else {
			a.aiBootState = aiBootNone
		}
	}
}

// aiDownloadEnvOff reports whether the automatic (DLL-only) startup download
// is disabled; tests and CI set MYLABEL_NO_SAM_BOOTSTRAP because Detect
// never succeeds under the test binary and the goroutine must not touch the
// network there.
func aiDownloadEnvOff() bool { return os.Getenv("MYLABEL_NO_SAM_BOOTSTRAP") != "" }
