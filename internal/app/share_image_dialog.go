package app

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/locales"
)

// Lody's chat-share-image-dialog: a preview, not an editor. Four controls,
// shape row then surface row (where the image goes and the mat; the
// ground and the palette), and two actions, Copy image and Export PNG. It
// completes only on a real copy or save: a cancelled file picker closes
// nothing and clears no selection.

// shareImageClipboard puts PNG bytes on the clipboard; tests replace it.
var shareImageClipboard = func(data []byte) error {
	return mygo.Clipboard.Write(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, data))))
}

// shareImageSaveDialog asks where to save, "" when the user backed out;
// tests replace it.
var shareImageSaveDialog = func(win *mygo.Window, name string) (string, error) {
	return mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, DefaultPath: name})
}

// shareSpec is the card the preview shows, and the export captures.
func (st *shareImage) spec() shareCardSpec {
	return shareCardSpec{messages: st.target.messages, title: st.target.title, dest: st.dest, mat: float32(st.mat),
		dark: st.dark, backdrop: st.backdrop, meta: st.target.meta}
}

// shareImageDialog is the preview, over everything, while it is open.
func (a *App) shareImageDialog(c *ui.Context, p *palette) {
	st := &a.shareImg
	if !st.open {
		return
	}
	w, h := c.Size()
	open := true
	busy := st.busy != ""
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		sheetIn(backdrop, panel)
		// The modal rung: Lody's overlay, the elevated surface under the
		// large shadow.
		if p.Dark {
			backdrop.Background(ui.RGBA(0, 0, 0, 0.6))
			panel.Background(p.Ground).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.06)).
				Shadow(0, 0, 0, 0.5, ui.RGBA(255, 255, 255, 0.08)).Shadow(0, 24, 56, -8, ui.RGBA(0, 0, 0, 0.7))
		} else {
			backdrop.Background(ui.RGBA(28, 28, 28, 0.5))
			panel.Background(p.Popover).Shadow(0, 0, 0, 0.5, ui.RGBA(28, 28, 28, 0.08)).
				Shadow(0, 2, 6, -2, ui.RGBA(28, 28, 28, 0.08)).Shadow(0, 24, 56, -12, ui.RGBA(28, 28, 28, 0.28))
		}
		// Wider than a dialog's column of prose: the card at a readable scale.
		panel.Width(min(720, w-32)).MaxHeight(h - 32).Padding(16).Gap(16).Radius(14).Label(locales.ShareImgDialogTitle())

		ui.Column(c.Key("share-head")).FillWidth().Gap(6).Shrink(0).Padding(0, 36, 0, 0).Children(func() {
			ui.Text(c, locales.ShareImgDialogTitle()).FontSize(16).LineHeight(24.0 / 16).FontWeight(600).TextColor(p.Text)
			ui.Text(c, locales.ShareImgDialogDescription()).FontSize(sizeBody).LineHeight(20.0 / 14).TextColor(p.Secondary)
		})

		// The preview is a block inside the panel, the region fill on the
		// panel's own padding.
		ui.Column(c.Key("share-preview")).FillWidth().Grow(1).Shrink(1).MinHeight(0).Padding(24).Radius(10).
			Background(p.Text.Alpha(0.03)).Children(func() {
			if len(st.target.messages) == 0 {
				ui.Text(c, locales.ShareImgEmpty()).FontSize(sizeBody).TextColor(p.Secondary).Margin(ui.Auto)
				return
			}
			a.sharePreview(c, st.spec())
		})

		// Controls above actions: shape, then surface. The mat is inert
		// without a ground, and stays where it is.
		ui.Column(c.Key("share-controls")).FillWidth().Gap(12).Shrink(0).Children(func() {
			ui.Row(c.Key("shape")).FillWidth().Wrap().AlignItems(ui.Center).GapX(20).GapY(12).Children(func() {
				shareLabelled(c, p, locales.ShareImgDestination(), busy, func() {
					st.shareTrack(c, p, "dest", []option{{"chat", locales.ShareImgDestinationChat()}, {"post", locales.ShareImgDestinationPost()}},
						st.dest, 56, busy, func(id string) {
							// A size is a starting point: it seeds the mat again.
							st.dest, st.mat = id, float64(shareDefaultMat[id])
						})
				})
				shareMatSlider(c, p, &st.mat, busy || st.backdrop == "none")
			})
			ui.Row(c.Key("surface")).FillWidth().Wrap().AlignItems(ui.Center).GapX(20).GapY(12).Children(func() {
				ui.Row(c.Key("swatches")).Gap(6).AlignItems(ui.Center).Role(ui.RoleRadioGroup).Label(locales.ShareImgBackdrop()).Children(func() {
					for _, b := range shareBackdrops {
						if shareSwatch(c, p, b, st.backdrop == b, busy).Clicked() && !busy {
							st.backdrop = b
						}
					}
				})
				st.shareTrack(c, p, "theme", []option{{"light", locales.ShareImgThemeLight()}, {"dark", locales.ShareImgThemeDark()}},
					map[bool]string{true: "dark", false: "light"}[st.dark], 64, busy, func(id string) { st.dark = id == "dark" }).
					Label(locales.ShareImgTheme())
			})
		})

		ui.Row(c.Key("share-foot")).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.End).Shrink(0).Children(func() {
			switch {
			case st.failed:
				ui.Text(c, locales.ShareImgExportFailed()).FontSize(sizeSub).LineHeight(18.0 / 13).TextColor(p.Danger).Grow(1).Role(ui.RoleStatus)
			case st.copied:
				ui.Text(c, locales.ShareImgCopied()).FontSize(sizeSub).LineHeight(18.0 / 13).TextColor(p.Secondary).Grow(1).Role(ui.RoleStatus)
			}
			ready := len(st.target.messages) > 0 && !busy
			copyIcon, copyLabel := iconCopy, locales.ShareImgCopyImage()
			if st.copied {
				copyIcon = iconCheck
			}
			if st.busy == "copy" {
				copyIcon, copyLabel = nil, locales.ShareImgCopying()
			}
			if lodyButton(c, p, "secondary", "copy", copyIcon, copyLabel, ready, st.busy == "copy").Clicked() && ready {
				a.runShareImage("copy")
			}
			saveIcon, saveLabel := iconDownload, locales.ShareImgExportPng()
			if st.busy == "export" {
				saveIcon, saveLabel = nil, locales.ShareImgExporting()
			}
			if lodyButton(c, p, "primary", "export", saveIcon, saveLabel, ready, st.busy == "export").Clicked() && ready {
				a.runShareImage("export")
			}
		})

		x := lodyButton(c, p, "ghost", "share-close", iconX, "", true).Absolute().Top(16).Right(16).Label(locales.Close())
		if x.Clicked() && !busy {
			st.open = false
		}
	})
	// Nothing dismisses it while an image is on its way.
	if !open && !busy {
		st.open = false
	}
}

// sharePreview fits the card to the panel's width, so it stays readable;
// a taller card scrolls rather than shrinking to a thumbnail. The card
// keeps its own layout: only the preview's scale changes.
func (a *App) sharePreview(c *ui.Context, s shareCardSpec) {
	st := &a.shareImg
	scroller := ui.Scroll(c.Key("share-fit")).FillWidth().Grow(1).MinHeight(0)
	avail := scroller.Bounds().W
	scroller.Children(func() {
		nat := st.natural
		scale := float32(1)
		if nat.W > 0 && avail > 0 {
			scale = min(1, avail/nat.W)
		}
		spacer := ui.Box(c.Key("spacer")).AlignSelf(ui.Center).Shrink(0)
		if nat.W > 0 {
			spacer.Size(nat.W*scale, nat.H*scale)
		} else {
			spacer.Opacity(0)
		}
		spacer.Children(func() {
			card := shareCard(c, s).TransformOrigin(0, 0).Scale(scale, scale)
			if lb := card.LayoutBounds(); lb.W > 0 && (abs32(lb.W-nat.W) > 0.5 || abs32(lb.H-nat.H) > 0.5) {
				st.natural = ui.Rect{W: lb.W, H: lb.H}
				c.AnimationFrame()
			}
		})
	})
}

// runShareImage starts a copy or an export: the button shows it running
// for a frame, then the card is captured, as the preview shows it.
func (a *App) runShareImage(op string) {
	st := &a.shareImg
	st.busy, st.failed, st.copied = op, false, false
	spec, title, win := st.spec(), st.target.title, a.win
	fail := func() {
		st.busy, st.failed = "", true
	}
	a.update(func() {
		img := renderShareImage(spec)
		var buf bytes.Buffer
		if img == nil || png.Encode(&buf, img) != nil {
			fail()
			return
		}
		data := buf.Bytes()
		if op == "copy" {
			if err := shareImageClipboard(data); err != nil {
				fail()
				return
			}
			st.copied = true
			a.shareImageEnded("copied")
			return
		}
		go func() {
			path, err := shareImageSaveDialog(win, shareFileName(title, "lody-conversation"))
			if err == nil && path != "" {
				err = os.WriteFile(path, data, 0o644)
			}
			a.update(func() {
				switch {
				case err != nil:
					fail()
				case path == "":
					// Backing out of the picker is no share: everything stays.
					st.busy = ""
				default:
					a.shareImageEnded("saved")
				}
			})
		}()
	})
}

var shareFileUnsafe = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f\x7f-\x9f]`)

// shareFileName is the PNG's name: the session's title made safe for a
// file system, or fallback.
func shareFileName(title, fallback string) string {
	name := strings.TrimSpace(title)
	if name == "" {
		name = fallback
	}
	name = shareFileUnsafe.ReplaceAllString(name, "-")
	name = strings.TrimRight(name, ". ")
	if utf8.RuneCountInString(name) > 120 {
		name = string([]rune(name)[:120])
	}
	if name == "" {
		name = fallback
	}
	return name + ".png"
}

// shareLabelled is a control with its footnote label before it, dimmed
// with it while it is inert.
func shareLabelled(c *ui.Context, p *palette, label string, dim bool, control func()) {
	ui.Row(c.Key("labelled/" + label)).Gap(8).AlignItems(ui.Center).Children(func() {
		t := ui.Text(c, label).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Secondary).Shrink(0)
		t.Opacity(t.AnimateWith("dim", map[bool]float32{true: 0.45, false: 1}[dim], durFast, easeStandard))
		control()
	})
}

// shareTrack is a two-way choice as @lody/ui's Tabs draw one: a flat tray,
// its two keys as wide as each other, the chosen one standing on it.
func (st *shareImage) shareTrack(c *ui.Context, p *palette, key string, options []option, selected string, minW float32, disabled bool, pick func(string)) ui.Element {
	if st.trackW == nil {
		st.trackW = map[string]float32{}
	}
	w := max(minW, st.trackW[key]+24)
	tray := ui.RGBA(28, 28, 28, 0.06)
	raised := ui.Hex("#FFFFFF")
	if p.Dark {
		tray, raised = ui.RGBA(255, 255, 255, 0.06), ui.Hex("#363636")
	}
	track := ui.Row(c.Key("track/" + key)).Height(32).Padding(2).Radius(10).Background(tray).Shrink(0).Role(ui.RoleRadioGroup)
	widest := float32(0)
	track.Children(func() {
		for _, o := range options {
			id := o.id
			on := id == selected
			seg := ui.ButtonBase(c.Key(o.id)).Width(w).Height(28).Radius(8).Center().Label(o.label).Role(ui.RoleRadio).Checked(on).
				Disabled(disabled).Transition(colorFade)
			if !disabled {
				seg.Cursor(ui.CursorPointer)
			} else {
				seg.Opacity(0.45)
			}
			ink := p.Secondary
			if on || seg.Hovered() && !disabled {
				ink = p.Text
			}
			if on {
				raisedLook(seg, p, raised)
			}
			seg.Children(func() {
				t := ui.Text(c, o.label).FontSize(sizeSub).FontWeight(500).LetterSpacing(-0.13).TextColor(ink).SingleLine()
				widest = max(widest, t.LayoutBounds().W)
			})
			if seg.Clicked() && !disabled && !on {
				pick(id)
			}
		}
	})
	if widest > 0 && widest != st.trackW[key] {
		st.trackW[key] = widest
		c.AnimationFrame()
	}
	return track
}

// raisedLook stands a fill on the surface: the raised sheen over it, and
// the raised shadow under it.
func raisedLook(e ui.Element, p *palette, fill ui.Color) {
	if p.Dark {
		e.Gradient(fill.Mix(ui.Hex("#FFFFFF"), 0.04), fill, 180).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.08)).
			Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.7)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.5))
		return
	}
	e.Gradient(fill, fill.Mix(ui.Hex("#1C1C1C"), 0.03), 180).Shadow(0, 0, 0, 0.5, ui.RGBA(28, 28, 28, 0.16)).
		Shadow(0, 1, 1, 0, ui.RGBA(28, 28, 28, 0.06)).Shadow(0, 2, 4, -1, ui.RGBA(28, 28, 28, 0.07))
}

var iconSlash = lucide(`<path d="M22 2 2 22"/>`)

// shareSwatch is a ground to pick, painted with it: raised, no edge of its
// own, the accent ring saying which is chosen; none is drawn as an absent
// ground.
func shareSwatch(c *ui.Context, p *palette, backdrop string, on, disabled bool) ui.Element {
	label := map[string]string{
		"none": locales.ShareImgBackdropNone(), "lody": locales.ShareImgBackdropLody(), "welcome": locales.ShareImgBackdropWelcome(),
		"aurora": locales.ShareImgBackdropAurora(), "ocean": locales.ShareImgBackdropOcean(), "sunset": locales.ShareImgBackdropSunset(),
	}[backdrop]
	b := ui.ButtonBase(c.Key("swatch/"+backdrop)).Size(28, 28).Radius(8).Shrink(0).Background(raisedGround(p)).Label(label).
		Role(ui.RoleRadio).Checked(on).Disabled(disabled).Transition(colorFade)
	lodyTooltip(c, p, b, label)
	accent := p.Primary
	switch {
	case on:
		b.Shadow(0, 0, 0, 2, accent)
	case b.Hovered() && !disabled:
		b.Shadow(0, 0, 0, 2, accent.Alpha(0.4))
	}
	if p.Dark {
		b.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.08)).Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.7)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.5))
	} else {
		b.Shadow(0, 0, 0, 0.5, ui.RGBA(28, 28, 28, 0.16)).Shadow(0, 1, 1, 0, ui.RGBA(28, 28, 28, 0.06)).Shadow(0, 2, 4, -1, ui.RGBA(28, 28, 28, 0.07))
	}
	if disabled {
		b.Opacity(0.45)
	} else {
		b.Cursor(ui.CursorPointer)
	}
	if backdrop != "none" {
		b.Draw(func(pt *ui.Painter, r ui.Rect) { paintShareBackdrop(pt, r, backdrop) })
		b.Clip()
	}
	b.Children(func() {
		if backdrop == "none" {
			ui.Icon(c, iconSlash).Size(14, 14).TextColor(p.Tertiary)
		}
		if on {
			ui.Row(c.Key("checked")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().Background(ui.RGBA(0, 0, 0, 0.15)).Children(func() {
				ui.Icon(c, iconCheck).Size(14, 14).TextColor(ui.Hex("#FFFFFF"))
			})
		}
	})
	return b
}

// shareMatSlider is the mat, a slider over pixels on the template's 4px
// grid, its readout the pixel count so the look can be made again.
func shareMatSlider(c *ui.Context, p *palette, mat *float64, disabled bool) {
	ui.Row(c.Key("mat")).Grow(1).Shrink(1).MinWidth(208).Gap(12).AlignItems(ui.Center).Children(func() {
		dim := map[bool]float32{true: 0.45, false: 1}[disabled]
		label := ui.Text(c, locales.ShareImgMat()).FontSize(sizeFoot).LineHeight(16.0 / 12).TextColor(p.Secondary).Shrink(0)
		label.Opacity(label.AnimateWith("dim", dim, durFast, easeStandard))
		s := ui.SliderBase(c.Key("slider"), mat, shareMinMat, shareMaxMat).Step(shareMatStep).Height(20).Grow(1).Shrink(1).MinWidth(96).
			Padding(0, 8).Label(locales.ShareImgMat()).Disabled(disabled)
		if disabled {
			s.Opacity(0.6)
		} else {
			s.Cursor(ui.CursorPointer)
		}
		hover := s.Hovered() && !disabled
		muted := ui.Hex("#E4E4E4")
		if p.Dark {
			muted = p.Canvas
		}
		v := *mat
		s.Draw(func(pt *ui.Painter, r ui.Rect) {
			frac := float32((v - shareMinMat) / (shareMaxMat - shareMinMat))
			ty := r.Y + r.H/2 - 2
			pt.Fill(ui.Rect{X: r.X, Y: ty, W: r.W, H: 4}, muted, 2)
			if fill := r.W * frac; fill > 0 {
				pt.Fill(ui.Rect{X: r.X, Y: ty, W: fill, H: 4}, p.Primary, 2)
				if fill > 2 {
					pt.Fill(ui.Rect{X: r.X + fill - 2, Y: ty, W: 2, H: 4}, p.Primary, 0)
				}
			}
			cx := r.X + 8 + (r.W-16)*frac
			thumb := ui.Rect{X: cx - 8, Y: r.Y + r.H/2 - 8, W: 16, H: 16}
			pt.Shadow(thumb, 8, 0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.05))
			pt.Fill(thumb, p.Canvas, 8)
			edge := p.Border
			if hover {
				edge = p.Primary.Alpha(0.6)
			}
			pt.Stroke(thumb, edge, 8, 1)
		})
		value := ui.Text(c, fmt.Sprint(int(*mat))).Font(fontMono).FontSize(sizeFoot).FontFeatures("tnum").TextColor(p.Secondary).
			Width(28).TextAlign(ui.End).Shrink(0)
		value.Opacity(value.AnimateWith("dim", dim, durFast, easeStandard))
	})
}

// lodyButton is @lody/ui's Button at the small step: primary in ink,
// secondary raised, ghost bare; a busy one shows its spinner in place of
// its icon.
func lodyButton(c *ui.Context, p *palette, variant, key string, icon *ui.SVG, label string, enabled bool, busy ...bool) ui.Element {
	b := ui.ButtonBase(c.Key("btn/" + key)).Height(28).Radius(8).Gap(6).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).
		Label(label).Disabled(!enabled).Transition(colorFade)
	if label == "" {
		b.Width(28)
	} else {
		b.Padding(0, 10)
	}
	hovered := b.Hovered() && enabled
	pressed := b.Pressed() && enabled
	ink := p.Text
	switch variant {
	case "primary":
		fill := p.Text
		if hovered {
			fill = fill.Mix(p.Canvas, 0.12)
		}
		ink = p.Canvas
		switch {
		case pressed:
			b.Background(fill)
		case p.Dark:
			b.Gradient(fill, fill.Mix(ui.Hex("#000000"), 0.08), 180).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.55)).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.5))
		default:
			b.Gradient(fill.Mix(ui.Hex("#FFFFFF"), 0.1), fill, 180).InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.18)).Shadow(0, 1, 1.5, 0, ui.RGBA(28, 28, 28, 0.18))
		}
	case "secondary":
		fill := raisedGround(p)
		if p.Dark {
			fill = p.Popover
		}
		if hovered {
			fill = fill.Mix(p.Text, 0.04)
		}
		if pressed {
			b.Background(fill).Shadow(0, 0, 0, 0.5, p.Text.Alpha(0.14)).InsetShadow(0, 1, 1, 0, p.Text.Alpha(0.14))
		} else {
			raisedLook(b, p, fill)
		}
	default:
		ink = p.Secondary
		if hovered {
			b.Background(p.Text.Alpha(0.06))
			ink = p.Text
		}
	}
	if enabled {
		b.Cursor(ui.CursorPointer)
		pressable(b, 0.97)
	} else {
		b.Opacity(0.45)
	}
	b.Children(func() {
		switch {
		case len(busy) > 0 && busy[0]:
			spinner(c).Size(16, 16)
		case icon != nil:
			ui.Icon(c, icon).Size(16, 16).TextColor(ink)
		}
		if label != "" {
			ui.Text(c, label).FontSize(sizeSub).FontWeight(500).LetterSpacing(-0.13).TextColor(ink).SingleLine()
		}
	})
	return b
}
