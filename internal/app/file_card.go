package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

// A file in a conversation is Lody's file card: a small sheet of paper,
// its top-right corner folded, on a tinted tray, with the name, then its
// type and size, and an action. The sheet prints no glyph, only faint marks
// of ink drawn by the kind of file. A card that opens something lifts its
// sheet a pixel and tilts it 2° on hover, over 180ms; a file still on its
// machine, or expired after 90 days, dims it. A text file opens a preview;
// any other downloads.

const (
	fileRetention    = 90 * 24 * time.Hour
	filePreviewBytes = 1 << 20
	sheetW, sheetH   = 27.5, 37.4 // the caption size times 2.5 and 3.4
	sheetFold        = 8.8        // the caption size times 0.8
)

// fileKinds are the extensions of each kind of file, as Lody's.
var fileKinds = map[string]string{}

func init() {
	for kind, exts := range map[string]string{
		"text":        "txt text log md markdown mdx rst",
		"code":        "js mjs cjs jsx ts tsx mts cts py rb rs go java kt kts scala swift c h cc cpp cxx hpp hh m mm cs php pl pm lua r dart ex exs erl clj cljs hs elm vue svelte sh bash zsh fish sql graphql gql proto html htm css scss sass less diff patch",
		"data":        "json jsonl ndjson yaml yml toml ini cfg conf xml env properties",
		"spreadsheet": "csv tsv xls xlsx ods",
		"archive":     "zip gz tgz tar rar 7z bz2 xz zst",
		"image":       "png jpg jpeg gif webp bmp svg ico avif heic",
		"audio":       "mp3 wav ogg flac m4a aac opus",
		"video":       "mp4 mov webm mkv avi m4v",
		"document":    "pdf doc docx odt rtf",
	} {
		for _, e := range strings.Fields(exts) {
			fileKinds[e] = kind
		}
	}
}

// fileExt is a file name's extension, lowercased, "" without one.
func fileExt(name string) string {
	base := filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 || dot == len(base)-1 {
		return ""
	}
	return strings.ToLower(base[dot+1:])
}

// fileKind is a file's coarse kind, by its extension, then its media type.
func fileKind(name, mime string) string {
	if k := fileKinds[fileExt(name)]; k != "" {
		return k
	}
	mime = strings.ToLower(mime)
	switch {
	case strings.HasPrefix(mime, "text/"):
		return "text"
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.Contains(mime, "json") || strings.Contains(mime, "xml") || strings.Contains(mime, "yaml"):
		return "data"
	case strings.Contains(mime, "zip") || strings.Contains(mime, "compressed") || strings.Contains(mime, "tar"):
		return "archive"
	case strings.Contains(mime, "pdf") || strings.Contains(mime, "word") || strings.Contains(mime, "document"):
		return "document"
	}
	return "binary"
}

// fileSize is a size as Lody prints it.
func fileSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	}
	return fmt.Sprintf("%.2f GB", float64(n)/1024/1024/1024)
}

var shortExt = regexp.MustCompile(`^[A-Za-z0-9]{1,4}$`)

// fileMeta is "PDF · 2.4 MB": what the file is, then how big; the size
// alone without a short extension.
func fileMeta(name string, size int64) string {
	base := filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if dot := strings.LastIndexByte(base, '.'); dot > 0 && shortExt.MatchString(base[dot+1:]) {
		return strings.ToUpper(base[dot+1:]) + " · " + fileSize(size)
	}
	return fileSize(size)
}

// fileState is how a file card stands: pending, expired, previewable or
// downloadable.
func fileState(it session.Item, now time.Time) string {
	switch {
	case it.Transport == "local":
		return "pending"
	case it.UploadedAt > 0 && now.After(time.UnixMilli(int64(it.UploadedAt)).Add(fileRetention)):
		return "expired"
	case it.TextPreview:
		return "previewable"
	}
	return "downloadable"
}

// fileCards lists a message's files, at its speaker's side.
func (a *App) fileCards(c *ui.Context, p *palette, sessionID string, files []session.Item, end bool) {
	col := ui.Column(c.Key("files")).FillWidth().Gap(8)
	if end {
		col.AlignItems(ui.End).Padding(4, 8, 0, 8)
	} else {
		col.AlignItems(ui.Start)
	}
	col.Children(func() {
		for _, f := range files {
			a.fileCard(c, p, sessionID, f)
		}
	})
}

// fileCard is one file: its sheet, name and type, and its action.
func (a *App) fileCard(c *ui.Context, p *palette, sessionID string, it session.Item) {
	state := fileState(it, time.Now())
	live := state == "previewable" || state == "downloadable"
	downloading := a.downloads[it.FileID]
	subtitle := fileMeta(it.FileName, it.SizeBytes)
	switch state {
	case "pending":
		subtitle = locales.FileUploading()
		if m := a.machineName(it.FileMachine); it.FileMachine != "" && m != "" {
			subtitle = locales.FileUploadingFrom(m)
		}
	case "expired":
		subtitle = locales.FileExpired(fileSize(it.SizeBytes))
	}
	name := firstNonEmpty(it.FileName, locales.File())
	tray := secondaryGround(p)
	card := ui.Row(c.Key("file/" + it.FileID)).FillWidth().MaxWidth(384).Radius(10).Gap(4).Clip().Background(tray).Transition(colorFade)
	card.Children(func() {
		main := ui.ButtonBase(c.Key("main")).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Padding(6, 6, 6, 8).Radius(10).Label(name)
		hovered := live && main.Hovered()
		if live {
			main.Cursor(ui.CursorPointer).OnClick(func() {
				if state == "previewable" {
					a.openFilePreview(sessionID, it)
				} else {
					a.downloadFile(sessionID, it)
				}
			})
			if hovered {
				card.Background(tray.Mix(p.Text, 0.04))
			}
		}
		// Pressed, the card sinks to .99 over 120ms on ease.standard, its
		// transform transitioned as its fill.
		press := float32(1)
		if live && main.Pressed() {
			press = 0.99
		}
		card.AnimatePose(ui.Pose{Scale: press}, ui.Animation{Duration: durFast, Ease: easeStandard})
		main.Children(func() {
			fileSheet(c, p, fileKind(it.FileName, it.MimeType), live && hovered, !live)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, name).FontSize(sizeSub).FontWeight(500).TextColor(p.Text).SingleLine()
				tip(c, p, ui.Text(c, subtitle).FontSize(sizeFoot).TextColor(p.Secondary).FontFeatures("tnum").SingleLine(), subtitle)
			})
			// The action chip's ink and well fade over 120ms on CSS's ease,
			// as Lody's (a duration, no curve of its own).
			chip := ui.Box(c.Key("action")).Size(28, 28).Radius(5).Center().Shrink(0).Transition(chipFade)
			ink := p.Secondary
			if hovered {
				ink = p.Text
				chip.Background(wellGround(p))
			}
			ink = chip.AnimateColor("ink", ink, chipInk)
			chip.Children(func() {
				switch {
				case state == "pending" || state == "downloadable" && downloading:
					spinner(c).Size(16, 16)
				case state == "expired":
					ui.Icon(c, iconClock).Size(16, 16).TextColor(ink)
				case state == "downloadable":
					ui.Icon(c, iconDownload).Size(16, 16).TextColor(ink)
				default:
					ui.Icon(c, iconEye).Size(16, 16).TextColor(ink)
				}
			})
		})
		if state == "previewable" && isHTMLFile(it) {
			// An HTML page's preview has no control for its source: it
			// downloads beside it.
			dl := ui.ButtonBase(c.Key("download")).Size(28, 28).Radius(5).Center().Shrink(0).AlignSelf(ui.Center).Margin(0, 6, 0, 0).
				Label(locales.FileDownloadAction()).Disabled(downloading).Transition(chipFade)
			tip(c, p, dl, locales.FileDownloadAction())
			ink := p.Secondary
			if dl.Hovered() && !downloading {
				ink = p.Text
				dl.Background(wellGround(p))
			}
			ink = dl.AnimateColor("ink", ink, chipInk)
			if downloading {
				dl.Opacity(0.6)
			} else {
				dl.Cursor(ui.CursorPointer).OnClick(func() { a.downloadFile(sessionID, it) })
			}
			dl.Children(func() {
				if downloading {
					spinner(c).Size(16, 16)
					return
				}
				ui.Icon(c, iconDownload).Size(16, 16).TextColor(ink)
			})
		}
	})
}

// chipFade and chipInk fade a file card's action chip: 120ms on CSS's
// ease, as Lody's transition with a duration and no curve.
var (
	chipFade = ui.ElementTransition{Colors: true, Duration: durFast, Ease: cssEase}
	chipInk  = ui.Animation{Duration: durFast, Ease: cssEase}
)

// isHTMLFile is whether a file is an HTML page.
func isHTMLFile(it session.Item) bool {
	ext := fileExt(it.FileName)
	mime, _, _ := strings.Cut(strings.ToLower(it.MimeType), ";")
	return ext == "html" || ext == "htm" || strings.TrimSpace(mime) == "text/html"
}

// secondaryGround is Lody's secondaryBackground: the tray a file lies on.
func secondaryGround(p *palette) ui.Color {
	if p.Dark {
		return ui.Hex("#161616")
	}
	return ui.Hex("#F9F9F9")
}

// raisedGround is Lody's raisedBackground: the paper.
func raisedGround(p *palette) ui.Color {
	if p.Dark {
		return ui.Hex("#232323")
	}
	return ui.Hex("#FFFFFF")
}

// wellGround is Lody's wellBackground, a chip sunk into its card.
func wellGround(p *palette) ui.Color {
	if p.Dark {
		return ui.RGBA(0, 0, 0, 0.28)
	}
	return ui.RGBA(28, 28, 28, 0.05)
}

// sheetLines are the marks of the ruled kinds: a width and an indent, as
// shares of the page; the first stops short of the fold.
var sheetLines = map[string][][2]float32{
	"lines": {{0.5, 0}, {1, 0}, {1, 0}, {1, 0}, {0.55, 0}},
	"code":  {{0.44, 0}, {0.62, 0.22}, {0.48, 0.22}, {0.70, 0.22}, {0.30, 0}},
	"data":  {{0.24, 0}, {0.56, 0.22}, {0.44, 0.22}, {0.60, 0.22}, {0.14, 0}},
}

// sheetArt is the marks each kind of file draws.
var sheetArt = map[string]string{
	"text": "lines", "document": "lines", "binary": "lines", "code": "code", "data": "data",
	"spreadsheet": "grid", "archive": "zip", "audio": "wave", "image": "frame", "video": "play",
}

// fileSheet is the paper of a file card. lifted picks it up, tilted, as
// the card's pointer does; muted dims it.
func fileSheet(c *ui.Context, p *palette, kind string, lifted, muted bool) {
	s := ui.Box(c.Key("sheet")).Size(sheetW, sheetH).Shrink(0).PassThrough()
	motion := ui.Animation{Duration: durRegular, Ease: easeStandard}
	k := s.AnimateTo("lift", b2f(lifted), motion)
	s.Translate(0, -k).Rotate(-2 * k)
	if muted {
		s.Opacity(0.55)
	}
	paper := raisedGround(p)
	label := p.Text
	ink, strong := label.Alpha(0.10), label.Alpha(0.18)
	flap := paper.Mix(label, 0.09)
	art := sheetArt[kind]
	s.Draw(func(pt *ui.Painter, r ui.Rect) {
		// The shadow follows the box, the cut corner too: Lody's card
		// shadow, blending to its raised one as it lifts.
		ring := ui.RGBA(28, 28, 28, 0.10+0.06*k)
		if p.Dark {
			ring = ui.RGBA(255, 255, 255, 0.07)
		}
		pt.Shadow(r, 5, 0, 0, 0, 0.5, ring)
		pt.Shadow(r, 5, 0, 1, 1.5+k*(-0.5), 0, ui.RGBA(0, 0, 0, 0.04+0.02*k))
		pt.Shadow(r, 5, 0, 2, 6-2*k, -2+k, ui.RGBA(0, 0, 0, 0.04+0.03*k))
		x, y, w, h, rad, f := r.X, r.Y, r.W, r.H, float32(5), float32(sheetFold)
		page := (&ui.Path{}).MoveTo(x+rad, y).LineTo(x+w-f, y).LineTo(x+w, y+f).LineTo(x+w, y+h-rad).
			QuadTo(x+w, y+h, x+w-rad, y+h).LineTo(x+rad, y+h).QuadTo(x, y+h, x, y+h-rad).
			LineTo(x, y+rad).QuadTo(x, y, x+rad, y).Close()
		pt.FillPath(page, paper)
		pt.FillPath((&ui.Path{}).MoveTo(x+w-f, y).LineTo(x+w-f, y+f).LineTo(x+w, y+f).Close(), flap)
		// The page inside its margins.
		cx, cy, cw, ch := x+4, y+6, w-8, h-12
		bar := func(bx, by, bw, bh float32, c ui.Color) {
			pt.Fill(ui.Rect{X: bx, Y: by, W: bw, H: bh}, c, min(bw, bh)/2)
		}
		switch art {
		case "lines", "code", "data":
			for i, l := range sheetLines[art] {
				bar(cx+l[1]*cw, cy+float32(i)*(ch-2)/4, l[0]*cw, 2, ink)
			}
		case "grid":
			gy, gh := cy+4, ch-4
			cellW, cellH := (cw-4)/3, (gh-6)/4
			for i := range 12 {
				ink := ink
				if i < 3 {
					ink = strong
				}
				pt.Fill(ui.Rect{X: cx + float32(i%3)*(cellW+2), Y: gy + float32(i/3)*(cellH+2), W: cellW, H: cellH}, ink, 1)
			}
		case "zip":
			gy, gh := cy+4, ch-4
			mid := cx + cw/2
			step := (gh - 4*2 - 5) / 4
			for i, d := range []float32{-2, 2, -2, 2} {
				bar(mid+d-2.5, gy+float32(i)*(2+step), 5, 2, ink)
			}
			pt.Fill(ui.Rect{X: mid - 3, Y: gy + gh - 5, W: 6, H: 5}, strong, 2)
		case "wave":
			gy, gh := cy+4, ch-4
			gap := (cw - 12) / 5
			for i, t := range []float32{0.35, 0.7, 1, 0.55, 0.8, 0.4} {
				bh := t * gh
				bar(cx+float32(i)*(2+gap), gy+(gh-bh)/2, 2, bh, ink)
			}
		case "frame", "play":
			gy := cy + 4
			ph := ch - 4 - 2*2 - 2*3
			pt.Fill(ui.Rect{X: cx, Y: gy, W: cw, H: ph}, ink, 2)
			if art == "play" {
				px, py := cx+cw/2-2.5, gy+ph/2-3
				pt.FillPath((&ui.Path{}).MoveTo(px, py).LineTo(px+5, py+3).LineTo(px, py+6).Close(), strong)
			}
			bar(cx, gy+ph+3, cw, 2, ink)
			bar(cx, gy+ph+3+2+3, 0.55*cw, 2, ink)
		}
	})
}

// downloadFile saves a file to the Downloads folder, as a browser does.
func (a *App) downloadFile(sessionID string, it session.Item) {
	if a.conn == nil || a.downloads[it.FileID] {
		return
	}
	if a.downloads == nil {
		a.downloads = map[string]bool{}
	}
	a.downloads[it.FileID] = true
	workspace, storage, name := a.conn.ID, firstNonEmpty(it.StorageSessionID, sessionID), firstNonEmpty(it.FileName, it.FileID)
	go func() {
		path, err := downloadTarget(name)
		if err == nil {
			var f *os.File
			if f, err = os.Create(path); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
				err = a.api.SessionFile(ctx, a.tokenNow(), workspace, storage, it.FileID, f)
				cancel()
				if cerr := f.Close(); err == nil {
					err = cerr
				}
				if err != nil {
					os.Remove(path)
				}
			}
		}
		a.update(func() {
			delete(a.downloads, it.FileID)
			if err != nil {
				a.toast("error", locales.FileDownloadFailed(name))
				return
			}
			a.toast("success", locales.FileDownloaded(filepath.Base(path)))
		})
	}()
}

// downloadTarget is where a download of name goes: the Downloads folder,
// numbered past what is there.
func downloadTarget(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Downloads")
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" || name == "" {
		name = "download"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, nil
		}
		path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// attachPreview is the preview dialog of a text file.
type attachPreview struct {
	open      bool
	session   string
	item      session.Item
	loading   bool
	err       string
	text      string
	truncated bool
	raw       bool
	copied    time.Time
}

// openFilePreview opens a text file's preview, reading its start.
func (a *App) openFilePreview(sessionID string, it session.Item) {
	md := fileExt(it.FileName)
	markdownFile := md == "md" || md == "markdown" || md == "mdx"
	a.attachPrev = attachPreview{open: true, session: sessionID, item: it, loading: true, raw: !markdownFile && !isHTMLFile(it)}
	if a.conn == nil {
		return
	}
	workspace, storage := a.conn.ID, firstNonEmpty(it.StorageSessionID, sessionID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		text, truncated, err := a.api.SessionFilePreview(ctx, a.tokenNow(), workspace, storage, it.FileID, it.SizeBytes, filePreviewBytes)
		a.update(func() {
			fp := &a.attachPrev
			if fp.item.FileID != it.FileID {
				return
			}
			fp.loading = false
			if err != nil {
				fp.err = locales.FilePreviewFailed()
				return
			}
			fp.text, fp.truncated = text, truncated
		})
	}()
}

// filePreviewDialog is the preview: the name and size, the rendered or raw
// text, a copy of it, and the download.
func (a *App) filePreviewDialog(c *ui.Context, p *palette) {
	fp := &a.attachPrev
	if !fp.open {
		return
	}
	it := fp.item
	ext := fileExt(it.FileName)
	markdownFile := !isHTMLFile(it) && (ext == "md" || ext == "markdown" || ext == "mdx")
	downloading := a.downloads[it.FileID]
	_, h := c.Size()
	sheet(c, p, &fp.open, 768, func() {
		ui.Column(c.Key("preview/" + it.FileID)).FillWidth().Padding(16).MinHeight(0).Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Padding(0, 0, 12, 0).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, it.FileName).FontSize(sizeBody).FontWeight(600).TextColor(p.Text).SingleLine()
					ui.Text(c, fileSize(it.SizeBytes)).FontSize(sizeFoot).TextColor(p.Secondary).FontFeatures("tnum")
				})
				ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
					if markdownFile {
						view := "rendered"
						if fp.raw {
							view = "raw"
						}
						ui.Box(c).Margin(0, 4, 0, 0).Children(func() {
							segmented(c, p, "preview-view", []option{{"rendered", locales.FilePreviewRendered()}, {"raw", locales.FilePreviewRaw()}}, view, func(v string) {
								fp.raw = v == "raw"
							})
						})
					}
					// Copy is always the raw text; it turns to a green check for
					// 1.5s.
					if since := time.Since(fp.copied); since < 1500*time.Millisecond {
						c.After(1500*time.Millisecond - since)
						ui.Box(c.Key("copied")).Size(28, 28).Center().Label(locales.Copy()).Children(func() {
							ui.Icon(c, iconCheck).Size(16, 16).TextColor(p.Success)
						})
					} else {
						iconButton(c.Key("copy"), p, iconCopy, locales.Copy(), p.Hover).Disabled(fp.loading || fp.err != "").OnClick(func() {
							writeClipboard(fp.text)
							fp.copied = time.Now()
						})
					}
					dl := iconButton(c.Key("download"), p, iconDownload, locales.FileDownload(), p.Hover).Disabled(downloading)
					dl.OnClick(func() { a.downloadFile(fp.session, it) })
					ui.Box(c).Size(1, 16).Margin(0, 2).Background(p.Border)
					iconButton(c.Key("close"), p, iconX, locales.Close(), p.Hover).OnClick(func() { fp.open = false })
				})
			})
			ui.Box(c).FillWidth().Height(1).Background(p.Border.Alpha(0.5))
			if !fp.loading && fp.err == "" && fp.truncated {
				warn := p.Warning
				ui.Row(c).FillWidth().Margin(12, 0, 0, 0).Padding(8, 12).Gap(12).Radius(8).AlignItems(ui.Center).
					Border(1, warn.Alpha(0.25)).Background(warn.Alpha(0.10)).Children(func() {
					ui.Icon(c, iconTriangleAlert).Size(16, 16).TextColor(warn).Shrink(0)
					ui.Text(c, locales.FilePreviewTruncated()).FontSize(sizeFoot).TextColor(warn).Grow(1).MinWidth(0)
					outlineButton(c, p, nil, locales.FileDownloadFull()).Disabled(downloading).OnClick(func() { a.downloadFile(fp.session, it) })
				})
			}
			body := ui.Scroll(c.Key("preview-body")).FillWidth().MaxHeight(h*0.55).Margin(12, 0, 0, 0)
			body.Children(func() {
				switch {
				case fp.loading:
					ui.Row(c).FillWidth().Padding(48, 0).Gap(8).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
						spinner(c).Size(16, 16)
						ui.Text(c, locales.FilePreviewLoading()).FontSize(sizeSub).TextColor(p.Secondary)
					})
				case fp.err != "":
					ui.Column(c).FillWidth().Padding(48, 0).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconTriangleAlert).Size(24, 24).TextColor(p.Secondary.Alpha(0.6))
						ui.Text(c, fp.err).FontSize(sizeSub).TextColor(p.Secondary)
						outlineButton(c, p, nil, locales.FileDownload()).OnClick(func() { a.downloadFile(fp.session, it) })
					})
				case markdownFile && !fp.raw:
					ui.Column(c).FillWidth().Padding(0, 2, 4, 2).Children(func() {
						markdown.View(c.Key("md"), fp.text, &a.md)
					})
				default:
					ui.Text(c, fp.text).Font(fontMono).FontSize(13).LineHeight(1.625).TextColor(p.Text).Selectable().
						FillWidth().Padding(12).Radius(8).Background(p.Text.Alpha(0.04))
				}
			})
		})
	})
}

// loadMarkdownImage loads an image a reply's Markdown shows: a web
// address, or an image inline as data. Agent-local paths do not load.
func loadMarkdownImage(src string) (ui.ImageSource, error) {
	if rest, ok := strings.CutPrefix(src, "data:"); ok {
		meta, data, ok := strings.Cut(rest, ",")
		if !ok || !strings.HasPrefix(meta, "image/") || !strings.HasSuffix(meta, ";base64") {
			return nil, fmt.Errorf("unsupported image")
		}
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(data), ""))
		if err != nil {
			return nil, err
		}
		return ui.DecodeBitmap(b)
	}
	u, err := url.Parse(src)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("unsupported image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image unavailable (%d)", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	if strings.Contains(res.Header.Get("Content-Type"), "svg") || strings.HasSuffix(strings.ToLower(u.Path), ".svg") {
		return ui.ParseSVG(b)
	}
	return ui.DecodeBitmap(b)
}
