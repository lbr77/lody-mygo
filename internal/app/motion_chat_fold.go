package app

import "github.com/egoist/mygo/ui"

// heightReveal builds fn below a disclosure while it is open, its height
// growing into view as k goes to 1 and shrinking away as it goes back,
// clipped and without fading, as @lody/ui's Collapsible.Panel (and Base
// UI's) animates only its height. k is how far it is open.
func heightReveal(c *ui.Context, k float32, open bool, fn func()) {
	if !open && k == 0 {
		return
	}
	clip := ui.Column(c).FillWidth().Shrink(0)
	var inner ui.Element
	clip.Children(func() {
		inner = ui.Column(c).FillWidth().Shrink(0).Children(fn)
	})
	if k < 1 {
		clip.ClipY().Height(k * inner.Bounds().H)
	}
}
