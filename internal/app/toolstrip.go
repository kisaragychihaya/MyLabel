package app

import (
	"github.com/egoist/mygo/ui"

	"mylabel/locales"
)

// The left tool strip (docs/phase6-design.md §2, §6.1): a Photoshop-style column
// of square icon buttons, one per tool, the active one highlighted. The pointer
// tool and the view tools (hand, zoom, marquee) sit above a divider, the creation
// tools (rectangle, polygon, rotated box) below it; a new tool joins the strip by
// joining toolSpecs.

const (
	// toolStripWidth is the strip's fixed width, in DIP: one icon button plus the
	// column's padding and border.
	toolStripWidth = 44
	// toolButtonDIP is the side of one square icon button, and toolIconDIP the
	// size of the icon inside it.
	toolButtonDIP = 32
	toolIconDIP   = 18
	// toolStripDividerAfter is the index in toolSpecs the divider follows: the
	// pointer and the three view tools, then the creation tools.
	toolStripDividerAfter = 4
)

// toolSpec is one button of the strip. text returns the button's accessible name
// and its tooltip, "名称 (快捷键)", and icon the drawing it shows, both read per
// frame: a package-level string would freeze the language the app started in,
// and the zoom tool's icon follows its direction (zoomOutNext).
type toolSpec struct {
	tool Tool
	key  string
	icon func(a *App) *ui.SVG
	text func() string
}

// fixedIcon is a toolSpec icon that does not depend on the app's state.
func fixedIcon(svg *ui.SVG) func(*App) *ui.SVG {
	return func(*App) *ui.SVG { return svg }
}

// zoomToolIcon is the zoom tool's button: a magnifier with a plus while the tool
// zooms in, with a minus while it zooms out. Clicking that button is what flips
// the pair, so the icon doubles as the switch
// (docs/phase6-design.md §3).
func zoomToolIcon(a *App) *ui.SVG {
	if a.zoomOutNext {
		return iconToolZoomOut
	}
	return iconToolZoomIn
}

// toolSpecs lists the strip's buttons from top to bottom.
var toolSpecs = []toolSpec{
	{ToolSelect, "toolSelect", fixedIcon(iconToolSelect), locales.ToolStripSelect},
	{ToolPan, "toolPan", fixedIcon(iconToolPan), locales.ToolStripPan},
	{ToolZoom, "toolZoom", zoomToolIcon, locales.ToolStripZoom},
	{ToolMarquee, "toolMarquee", fixedIcon(iconToolMarquee), locales.ToolStripMarquee},
	{ToolRect, "toolRect", fixedIcon(iconToolRect), locales.ToolStripRect},
	{ToolPoly, "toolPoly", fixedIcon(iconToolPoly), locales.ToolStripPoly},
	{ToolObb, "toolObb", fixedIcon(iconToolObb), locales.ToolStripObb},
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
	// The theme pads a button for a text label, which on a 32 DIP square leaves
	// its row narrower than the 18 DIP icon and pushes the drawing off center:
	// a square icon button drops the padding and centers the icon in both
	// directions instead (docs-zh/ui/button.md, docs-zh/ui/layout.md).
	btn = btn.Label(name).Tooltip(name).Padding(0).Center().Size(toolButtonDIP, toolButtonDIP)
	btn.Children(func() {
		ui.Icon(c, spec.icon(a)).FontSize(toolIconDIP)
	})
	if btn.Clicked() {
		// The zoom tool's button is also its direction switch: clicking the
		// active tool's button flips the magnifier instead of switching to a tool
		// that is already there, which chooseTool decides for the buttons and the
		// shortcuts alike (docs/phase6-design.md §3).
		a.chooseTool(spec.tool)
	}
}
