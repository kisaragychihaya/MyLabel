// MyLabel is an image annotation tool built on mygo native UI.
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mylabel/internal/app"
)

func main() {
	a := app.New()
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "MyLabel",
			Width:     1440,
			Height:    900,
			MinWidth:  960,
			MinHeight: 600,
			StateKey:  "main",
			Content:   ui.View(a.View),
		})
		a.SetWindow(win)
		win.OnClose(func(e *mygo.CloseEvent) {
			if a.ConfirmClose() {
				a.Close() // flush once more and release the database
				return
			}
			e.PreventDefault()
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
