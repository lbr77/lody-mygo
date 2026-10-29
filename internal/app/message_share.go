package app

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// Message share, as lody-ios offers it on a finished reply: copy the whole
// message, or make it an image — 540 DIPs wide at twice the pixels, on
// warm paper, always light, the model's name and Lody at its foot — to
// save or copy, of all its blocks or those picked.

const (
	shareWidth     = 540
	shareMaxHeight = 6000
)

var (
	sharePaper = ui.RGB(250, 248, 243)
	shareInk   = ui.RGB(46, 43, 38)
	shareFoot  = ui.RGB(107, 102, 92)
)

// shareBlock is a top-level block of the reply, to pick for the image.
type shareBlock struct {
	kind string
	src  string
}

type messageShare struct {
	open     bool
	model    string
	blocks   []shareBlock
	picked   []bool
	picking  bool
	image    *image.RGBA
	bitmap   *ui.Bitmap
	tooLarge bool
	err      string
}

// replyText is a reply's prose: its text blocks, as lody-ios shares them,
// never its process or tool output.
func replyText(e session.Entry) []string {
	var parts []string
	for _, b := range blocks(e.Items) {
		if b.text && strings.TrimSpace(b.items[0].Text) != "" {
			parts = append(parts, b.items[0].Text)
		}
	}
	return parts
}

// shareBlocks splits markdown into its top-level blocks.
func shareBlocks(parts []string) []shareBlock {
	var out []shareBlock
	for _, src := range parts {
		d := parser.Parse(src)
		for i := range d.Children(0) {
			b := d.Block(i)
			kind := "paragraph"
			switch b.Kind {
			case parser.Heading:
				kind = "heading"
			case parser.List:
				kind = "list"
			case parser.CodeBlock:
				kind = "code"
			case parser.Table:
				kind = "table"
			case parser.Quote:
				kind = "quote"
			case parser.ThematicBreak:
				kind = "divider"
			}
			out = append(out, shareBlock{kind: kind, src: strings.TrimRight(src[b.Start:b.End], "\n")})
		}
	}
	return out
}

func shareBlockLabel(kind string) string {
	switch kind {
	case "heading":
		return locales.MsgShareBlockHeading()
	case "list":
		return locales.MsgShareBlockList()
	case "code":
		return locales.MsgShareBlockCode()
	case "table":
		return locales.MsgShareBlockTable()
	case "quote":
		return locales.MsgShareBlockQuote()
	case "divider":
		return locales.MsgShareBlockDivider()
	}
	return locales.MsgShareBlockParagraph()
}

// messageActions is the reply's ⋯: copy it all, or make it an image.
func (a *App) messageActions(c *ui.Context, p *palette, e session.Entry) {
	parts := replyText(e)
	if !e.Finished || len(parts) == 0 {
		return
	}
	entry := e
	iconButton(c, p, iconMore, locales.MsgShareActions(), p.Hover).Size(24, 24).Menu(func(m *ui.Menu) {
		if m.Item(locales.MsgShareCopyMessage()).Chosen() {
			writeClipboard(strings.Join(parts, "\n\n"))
			a.toast("success", locales.MsgShareCopied())
		}
		if m.Item(locales.MsgShareGenerateImage()).Chosen() {
			a.openMessageShare(entry)
		}
	})
}

func (a *App) openMessageShare(e session.Entry) {
	bs := shareBlocks(replyText(e))
	picked := make([]bool, len(bs))
	for i := range picked {
		picked[i] = true
	}
	a.msgShare = messageShare{open: true, model: firstNonEmpty(e.Model, a.view.ModelID), blocks: bs, picked: picked}
	a.renderMessageShare()
}

// renderMessageShare draws the image of the picked blocks.
func (a *App) renderMessageShare() {
	ms := &a.msgShare
	var src []string
	for i, b := range ms.blocks {
		if ms.picked[i] {
			src = append(src, b.src)
		}
	}
	ms.image, ms.bitmap, ms.tooLarge, ms.err = nil, nil, false, ""
	if len(src) == 0 {
		return
	}
	text := strings.Join(src, "\n\n")
	if len(text) > 128<<10 || len(src) > 128 {
		ms.tooLarge = true
		return
	}
	img, ok := renderShareCard(text, ms.model)
	if !ok {
		ms.tooLarge = true
		return
	}
	ms.image = img
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err == nil {
		ms.bitmap, _ = ui.DecodeBitmap(buf.Bytes())
	}
}

// renderShareCard draws the reply on paper, as tall as it needs, then
// cuts it there; false when it would pass 6000 DIPs.
func renderShareCard(text, model string) (*image.RGBA, bool) {
	md := &markdown.Options{}
	base := ui.LightTheme()
	base.Font, base.FontSize = fontUI, 18
	t := markdown.DefaultTheme(base)
	t.Font, t.FontSize, t.CodeFont = fontUI, 18, fontMono
	t.Text, t.Strong = shareInk, shareInk
	md.Theme = t
	md.WrapCode = true
	// Code as the image shows it: its language and lines, no copy button.
	md.CodeBlock = func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).Background(t.CodeBlockBackground).Border(1, t.CodeBlockBorder).Radius(t.CodeBlockRadius).AlignSelf(ui.Stretch).Clip().Children(func() {
			if b.Language != "" {
				ui.Text(c, b.Language).FontSize(14).TextColor(t.Muted).Padding(8, 0, 0, 14)
			}
			b.Body(c)
		})
	}
	img := ui.Render(func(c *ui.Context) {
		ui.Column(c).Width(shareWidth).Background(sharePaper).Padding(56, 48, 40, 48).Gap(28).Children(func() {
			markdown.View(c, text, md)
			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, model).FontSize(11).TextColor(shareFoot)
				ui.Spacer(c)
				ui.Text(c, "Lody").FontSize(12).FontWeight(500).TextColor(shareFoot)
			})
		})
	}, shareWidth, shareMaxHeight, 2)
	// The card ends where the paper does.
	b := img.Bounds()
	paper := color.RGBA{250, 248, 243, 255}
	bottom := 0
	for y := b.Max.Y - 1; y >= b.Min.Y; y-- {
		if img.RGBAAt(b.Min.X+4, y) == paper {
			bottom = y + 1
			break
		}
	}
	if bottom == 0 || bottom >= b.Max.Y {
		return nil, false
	}
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), bottom))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out, true
}

func shareJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92})
	return buf.Bytes(), err
}

// messageShareDialog previews the image, picks its blocks, and saves or
// copies it.
func (a *App) messageShareDialog(c *ui.Context, p *palette) {
	ms := &a.msgShare
	if !ms.open {
		return
	}
	_, h := c.Size()
	sheet(c, p, &ms.open, 640, func() {
		sheetHeader(c, p, locales.MsgSharePreview(), "", func() { ms.open = false })
		ui.Scroll(c).FillWidth().MaxHeight(h-220).Padding(4, 20).Children(func() {
			switch {
			case ms.picking:
				count := 0
				for _, on := range ms.picked {
					if on {
						count++
					}
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(0, 0, 8, 0).Children(func() {
					ui.Text(c, locales.MsgShareSelectedCount(fmt.Sprint(count), fmt.Sprint(len(ms.picked)))).FontSize(sizeFoot).TextColor(p.Secondary).Grow(1)
					all := count < len(ms.picked)
					label := locales.MsgShareSelectAll()
					if !all {
						label = locales.MsgShareSelectNone()
					}
					ghostButton(c, p, label).OnClick(func() {
						for i := range ms.picked {
							ms.picked[i] = all
						}
					})
				})
				card(c, p, func() {
					for i, b := range ms.blocks {
						index := i
						ui.Row(c.Key(fmt.Sprint("block", i))).FillWidth().MinHeight(40).Padding(6, 12).Gap(10).AlignItems(ui.Center).Cursor(ui.CursorPointer).
							Label(shareBlockLabel(b.kind)).OnClick(func() { ms.picked[index] = !ms.picked[index] }).Children(func() {
							ui.Checkbox(c, &ms.picked[index], "").Label(shareBlockLabel(b.kind))
							ui.Text(c, shareBlockLabel(b.kind)).FontSize(sizeFoot).FontWeight(500).TextColor(p.Secondary).Width(56).Shrink(0)
							ui.Text(c, strings.Join(strings.Fields(b.src), " ")).FontSize(sizeSub).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
						})
					}
				})
			case ms.tooLarge:
				ui.Text(c, locales.MsgShareTooLarge()).FontSize(sizeSub).TextColor(p.Secondary).Padding(20, 0)
			case ms.image == nil:
				ui.Text(c, locales.MsgShareEmptySelection()).FontSize(sizeSub).TextColor(p.Secondary).Padding(20, 0)
			case ms.bitmap != nil:
				w, ih := ms.bitmap.Size()
				scale := min(1, 600/float32(w))
				ui.Image(c, ms.bitmap).Size(float32(w)*scale, float32(ih)*scale).Radius(8).Border(1, p.Border)
			}
		})
		ui.Row(c).FillWidth().Padding(12, 20, 16, 20).Gap(8).AlignItems(ui.Center).Children(func() {
			if ms.picking {
				primaryButton(c, p, locales.MsgShareDone(), true).OnClick(func() {
					ms.picking = false
					a.renderMessageShare()
				})
				return
			}
			ghostButton(c, p, locales.MsgShareSelectBlocks()).Border(1, p.Border).OnClick(func() { ms.picking = true })
			ui.Spacer(c)
			ready := ms.image != nil
			outlineButton(c, p, iconCopy, locales.MsgShareCopyImage()).Disabled(!ready).OnClick(func() {
				var buf bytes.Buffer
				if png.Encode(&buf, ms.image) == nil {
					mygo.Clipboard.Write(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, buf.Bytes()))))
					a.toast("success", locales.MsgShareImageCopied())
				}
			})
			primaryButton(c, p, locales.MsgShareSaveImage(), ready).OnClick(func() {
				img, win := ms.image, a.win
				go func() {
					path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: win, Title: locales.MsgShareSaveImage(), DefaultPath: "lody-reply.jpg"})
					if err != nil || path == "" {
						return
					}
					data, err := shareJPEG(img)
					if err == nil {
						err = os.WriteFile(path, data, 0o644)
					}
					a.update(func() {
						if err != nil {
							a.toast("danger", err.Error())
							return
						}
						a.toast("success", locales.MsgShareImageSaved())
					})
				}()
			})
		})
	})
}
