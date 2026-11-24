package app

import "github.com/egoist/mygo/ui"

// The tool strip's icons (docs/phase6-design.md §2): seven hand-drawn 24×24
// outlines, stroked in the element's text color, so the button paints them in
// the foreground of an ordinary button and of an active (primary) one alike.
//
// Each is its own drawing, drawn as outlines only: an arrow pointer for the
// select tool, a hand for the pan tool, a magnifier with a plus for the zoom
// tool, a dashed box for the marquee, a box for the rectangle tool, a pentagon
// for the polygon tool and a turned box for the rotated box tool. Adding a tool
// to the strip means adding one line here.

// toolIcon wraps an icon's shapes in the attributes every tool icon shares:
// 24×24, no fill, a 2 DIP round-capped currentColor stroke.
func toolIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"` +
		` fill="none" stroke="currentColor" stroke-width="2"` +
		` stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	// iconToolSelect is an arrow pointer: the tool that selects what is under it.
	iconToolSelect = toolIcon(`<path d="M4 3.5 11 20.5 13.5 13.5 20.5 11Z"/>` +
		`<path d="M13.5 13.5 20 20"/>`)
	// iconToolPan is a palm with three fingers up and a thumb along its side.
	iconToolPan = toolIcon(`<path d="M9 12V6.5a1.5 1.5 0 0 1 3 0V11"/>` +
		`<path d="M12 11V5.5a1.5 1.5 0 0 1 3 0V11"/>` +
		`<path d="M15 11.5V8a1.5 1.5 0 0 1 3 0v7.5a5 5 0 0 1-5 5h-1.5a5 5 0 0 1-5-5V12a1.5 1.5 0 0 1 3 0"/>`)
	// iconToolZoomIn is a magnifier holding a plus: the zoom tool's first
	// direction, and iconToolZoomOut's sibling.
	iconToolZoomIn = toolIcon(`<circle cx="10.5" cy="10.5" r="6.5"/>` +
		`<path d="M15.4 15.4 21 21"/>` +
		`<path d="M10.5 7.5v6M7.5 10.5h6"/>`)
	// iconToolZoomOut is the same magnifier holding a minus.
	iconToolZoomOut = toolIcon(`<circle cx="10.5" cy="10.5" r="6.5"/>` +
		`<path d="M15.4 15.4 21 21"/>` +
		`<path d="M7.5 10.5h6"/>`)
	// iconToolMarquee is a dashed box: the region a drag marks out.
	iconToolMarquee = toolIcon(`<rect x="3" y="5" width="18" height="14" rx="2" stroke-dasharray="3 3"/>`)
	// iconToolRect is a plain box.
	iconToolRect = toolIcon(`<rect x="3" y="5" width="18" height="14" rx="2"/>`)
	// iconToolPoly is a pentagon.
	iconToolPoly = toolIcon(`<path d="M12 3.5 20.5 9.8 17.3 20.5H6.7L3.5 9.8Z"/>`)
	// iconToolObb is a box turned off the axes.
	iconToolObb = toolIcon(`<rect x="4.5" y="8.5" width="15" height="7" rx="1.5" transform="rotate(-22 12 12)"/>`)
)
