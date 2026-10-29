package app

import (
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/catalog"
	"github.com/lbr77/lody-mygo/locales"
)

// A session's pull request, as Lody marks it: GitHub's glyph for its
// state in GitHub's hue, and the CI's verdict in a badge at its corner.

var (
	iconDot         = lucide(`<circle cx="12" cy="12" r="5" fill="currentColor"/>`)
	iconGitPR       = lucide(`<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M13 6h3a2 2 0 0 1 2 2v7"/><line x1="6" x2="6" y1="9" y2="21"/>`)
	iconGitPRDraft  = lucide(`<circle cx="5" cy="6" r="3"/><path d="M5 9v12"/><circle cx="19" cy="18" r="3"/><path d="m15 9-3-3 3-3"/><path d="M12 6h5a2 2 0 0 1 2 2v3"/><path d="M19 15v-2"/>`)
	iconGitMerge    = lucide(`<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/>`)
	iconGitPRClosed = lucide(`<circle cx="6" cy="6" r="3"/><path d="M6 9v12"/><path d="m21 3-6 6"/><path d="m21 9-6-6"/><path d="M18 11.5V15"/><circle cx="18" cy="18" r="3"/>`)
)

// prTone is a pull request state's glyph and hue, as GitHub draws them.
func prTone(p *palette, status string) (*ui.SVG, ui.Color) {
	switch status {
	case "merged":
		return iconGitMerge, map[bool]ui.Color{false: ui.Hex("#8250DF"), true: ui.Hex("#A371F7")}[p.Dark]
	case "closed":
		return iconGitPRClosed, map[bool]ui.Color{false: ui.Hex("#CF222E"), true: ui.Hex("#F85149")}[p.Dark]
	case "draft":
		return iconGitPRDraft, p.Secondary
	}
	return iconGitPR, map[bool]ui.Color{false: ui.Hex("#1A7F37"), true: ui.Hex("#3FB950")}[p.Dark]
}

func prStatusLabel(status string) string {
	switch status {
	case "merged":
		return locales.PrMerged()
	case "closed":
		return locales.PrClosed()
	case "draft":
		return locales.PrDraft()
	}
	return locales.PrOpen()
}

// prMark is a session's pull request mark: 12px in the sidebar, at a
// muted saturation (compact), with its CI's verdict cut into its corner.
func prMark(c *ui.Context, p *palette, s *catalog.Session, compact bool) bool {
	pr := s.LatestPR()
	if pr == nil {
		return false
	}
	icon, hue := prTone(p, pr.Status)
	if compact {
		hue = hue.Mix(p.Secondary, 0.35)
	}
	ci := s.PRState[pr.URL].CI
	label := prStatusLabel(pr.Status)
	if n := pr.PRNumber(); n > 0 {
		label = locales.PrNumbered(label, n)
	}
	size := float32(14)
	if compact {
		size = 12
	}
	ui.Box(c.Key("pr")).Size(size+2, size+2).Shrink(0).Label(label).Children(func() {
		ui.Icon(c, icon).Size(size, size).TextColor(hue)
		var verdict *ui.SVG
		var ink ui.Color
		switch ci {
		case "s":
			verdict, ink = iconCheck, p.Success
		case "f", "e":
			verdict, ink = iconX, p.Danger
		case "x":
			verdict, ink = iconDot, p.Warning
		case "p":
			ink = p.Warning
		default:
			return
		}
		// The badge sits on a disc of the ground, as Lody masks the glyph.
		ui.Row(c).Absolute().Right(-1).Bottom(-1).Size(9, 9).Radius(5).Background(p.Ground).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			if verdict == nil {
				ui.Box(c).Size(5, 5).Radius(3).Background(ink)
				return
			}
			ui.Icon(c, verdict).Size(8, 8).TextColor(ink)
		})
	})
	return true
}
