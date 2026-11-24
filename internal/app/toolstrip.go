package app

import (
	"github.com/egoist/mygo/ui"

	"mylabel/locales"
)

// The left tool strip (docs/phase6-design.md §2): a Photoshop-style column of
// square icon buttons, one per tool, the active one highlighted. The view tools
// (hand, zoom, marquee) sit above a divider, the annotation tools below it; a
// new tool joins the strip by joining toolSpecs.

const (
	// toolStripWidth is the strip's fixed width, in DIP: one icon button plus the
	// column's padding and border.
	toolStripWidth = 44
	// toolButtonDIP is the side of one square icon button, and toolIconDIP the
	// size of the icon inside it.
	toolButtonDIP = 32
	toolIconDIP   = 18
	// toolStripDividerAfter is the index in toolSpecs the divider follows: the
	// three view tools, then the annotation tools.
	toolStripDividerAfter = 3
)

// toolSpec is one button of the strip. text returns the button's accessible
// name and its tooltip, "名称 (快捷键)", read per frame: a package-level string
// would freeze the language the app started in.
type toolSpec struct {
	tool Tool
	key  string
	icon *ui.SVG
	text func() string
}

// toolSpecs lists the strip's buttons from top to bottom.
var toolSpecs = []toolSpec{
	{ToolPan, "toolPan", iconToolPan, locales.ToolStripPan},
	{ToolZoom, "toolZoom", iconToolZoom, locales.ToolStripZoom},
	{ToolMarquee, "toolMarquee", iconToolMarquee, locales.ToolStripMarquee},
	{ToolRect, "toolRect", iconToolRect, locales.ToolStripRect},
	{ToolPoly, "toolPoly", iconToolPoly, locales.ToolStripPoly},
	{ToolObb, "toolObb", iconToolObb, locales.ToolStripObb},
}

// toolStrip builds the left tool strip. Only its own column is painted here; the
// canvas beside it keeps its Grow(1) share of the window.
func (a *App) toolStrip(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(toolStripWidth).FillHeight().Padding(6).Gap(6).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		for i, spec := range toolSpecs {
			if i == toolStripDividerAfter {
				ui.Divider(c)
			}
			a.toolStripButton(c, spec)
		}
	})
}

// toolStripButton renders one icon button, highlighted while its tool is active
// (the same PrimaryButton/Button pair the toolbar's tool buttons used). The
// label carries the tool's name, so a button showing only an icon still has an
// accessible name, and the tooltip repeats it with the shortcut
// (docs-zh/ui/icon.md, docs/phase6-design.md §2).
func (a *App) toolStripButton(c *ui.Context, spec toolSpec) {
	name := spec.text()
	var btn ui.Element
	if a.tool == spec.tool {
		btn = ui.PrimaryButton(c.Key(spec.key), "")
	} else {
		btn = ui.Button(c.Key(spec.key), "")
	}
	btn = btn.Label(name).Tooltip(name).Size(toolButtonDIP, toolButtonDIP)
	btn.Children(func() {
		ui.Icon(c, spec.icon).FontSize(toolIconDIP)
	})
	if btn.Clicked() {
		a.setTool(spec.tool)
	}
}
