package app

import (
	"context"
	"fmt"
	"github.com/lbr77/lody-mygo/locales"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/session"
)

// pendingFile is a file attached to the composer, not uploaded yet.
type pendingFile struct {
	Path  string
	Name  string
	Image bool
	Size  int64
}

// remoteImage is a picture fetched for display, keyed by image id.
type remoteImage struct {
	bitmap  *ui.Bitmap
	loading bool
	failed  bool
}

// addFiles attaches files within Lody's limits, reporting what it refused.
func (a *App) addFiles(paths []string) {
	images, files := 0, 0
	for _, f := range a.attachments {
		if f.Image {
			images++
		} else {
			files++
		}
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		name := filepath.Base(path)
		switch {
		case err != nil || !info.Mode().IsRegular():
			a.sendError = locales.AttachNotFile(name)
			continue
		case info.Size() == 0:
			a.sendError = locales.AttachEmpty(name)
			continue
		case info.Size() > cloud.MaxFileBytes:
			a.sendError = locales.AttachTooLarge(name)
			continue
		case len(a.attachments) >= cloud.MaxAttachments:
			a.sendError = locales.AttachTooMany(cloud.MaxAttachments)
			return
		}
		f := pendingFile{Path: path, Name: name, Image: cloud.IsImage(path), Size: info.Size()}
		if f.Image && images >= cloud.MaxImages || !f.Image && files >= cloud.MaxFiles {
			a.sendError = locales.AttachTooManyKinds(cloud.MaxImages, cloud.MaxFiles)
			continue
		}
		if f.Image {
			images++
			a.loadLocalThumb(path)
		} else {
			files++
		}
		a.attachments = append(a.attachments, f)
	}
}

// pickFiles opens the system's file dialog. It waits for the user, so it
// runs off the main thread.
func (a *App) pickFiles() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: locales.AttachFiles(), Multiple: true})
		if err != nil || len(paths) == 0 {
			return
		}
		a.update(func() { a.addFiles(paths) })
	}()
}

// loadLocalThumb decodes a picture on disk for its chip.
func (a *App) loadLocalThumb(path string) {
	key := "file:" + path
	if _, ok := a.images[key]; ok {
		return
	}
	a.images[key] = &remoteImage{loading: true}
	go func() {
		var bm *ui.Bitmap
		data, err := os.ReadFile(path)
		if err == nil {
			bm, err = ui.DecodeBitmap(data)
		}
		a.update(func() { a.images[key] = &remoteImage{bitmap: bm, failed: err != nil} })
	}()
}

// attachmentChips shows the composer's attachments, each removable; while
// a message's attachments upload, those, each with its progress.
func (a *App) attachmentChips(c *ui.Context, p *palette) {
	files, uploading := a.attachments, false
	if a.sending && len(a.sendingFiles) > 0 && a.sendingID == a.selected {
		files, uploading = a.sendingFiles, true
	}
	if len(files) == 0 {
		return
	}
	// Progress moves as Lody's transition-[clip-path] and -[width] do.
	progress := func(f pendingFile) float32 { return min(100, max(0, a.fileProgress[f.Path])) }
	ui.Row(c).Wrap().Gap(8).Padding(2, 2, 6, 2).Children(func() {
		for i, f := range files {
			index := i
			remove := func() { a.attachments = append(a.attachments[:index:index], a.attachments[index+1:]...) }
			if uploading {
				remove = nil
			}
			if f.Image {
				img := a.images["file:"+f.Path]
				open := a.popover("peek/" + f.Path)
				thumb := ui.ButtonBase(c.Key(f.Path)).Size(56, 56).Radius(10).Clip().Background(p.Text.Alpha(0.05)).Label(f.Name).Cursor(ui.CursorPointer)
				thumb.OnClick(func() { *open = !*open })
				thumb.Children(func() {
					if img != nil && img.bitmap != nil {
						ui.Image(c, img.bitmap).Size(56, 56).Fit(ui.Cover)
					} else {
						ui.Box(c).Size(56, 56).Center().Children(func() { ui.Icon(c, iconImage).Size(20, 20).TextColor(p.Secondary) })
					}
					if uploading {
						// A wash over what is still to go, receding from the top.
						left := 56 * (100 - progress(f)) / 100
						ui.Box(c.Key("wash")).Absolute().Left(0).Right(0).Bottom(0).Height(left).Background(ui.RGBA(0, 0, 0, 0.45)).
							Transition(ui.ElementTransition{Size: true, Duration: 150 * time.Millisecond, Ease: ui.CubicBezier(0.4, 0, 0.2, 1)})
						return
					}
					removeButton(c, p, f.Name, remove)
				})
				if img != nil && img.bitmap != nil {
					a.imagePeek(c, p, thumb, open, img.bitmap, f.Name)
				}
				continue
			}
			chip := ui.Row(c.Key(f.Path)).Height(44).Padding(0, 32, 0, 10).Radius(10).Gap(8).AlignItems(ui.Center).Background(p.Text.Alpha(0.05)).MaxWidth(240)
			if uploading {
				chip.Clip() // the bar keeps to the chip's corners
			}
			chip.Children(func() {
				ui.Icon(c, iconFile).Size(18, 18).TextColor(p.Secondary)
				ui.Column(c).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, f.Name).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine()
					ui.Text(c, byteSize(f.Size)).FontSize(sizeCaption).TextColor(p.Secondary)
				})
				if uploading {
					ui.Box(c.Key("bar")).Absolute().Left(0).Right(0).Bottom(0).Height(4).Background(p.Text.Alpha(0.08)).Children(func() {
						ui.Box(c).Height(4).WidthPercent(progress(f)).Background(p.Primary).
							Transition(ui.ElementTransition{Size: true, Duration: 150 * time.Millisecond, Ease: ui.CubicBezier(0.4, 0, 0.2, 1)})
					})
					return
				}
				removeButton(c, p, f.Name, remove)
			})
		}
	})
}

// imagePeek is an attached image's preview: a card above its thumbnail,
// growing out of it with a little overshoot, as Lody's composer peek, so
// the draft stays in view while the image is checked.
func (a *App) imagePeek(c *ui.Context, p *palette, thumb ui.Element, open *bool, bm *ui.Bitmap, name string) {
	w, h := bm.Size()
	scale := min(1, 480/float32(max(w, 1)), 360/float32(max(h, 1)))
	pop := ui.Popover(c, thumb, open, func() {
		ui.Column(c).Gap(6).Padding(6).Children(func() {
			ui.Image(c, bm).Size(float32(w)*scale, float32(h)*scale).Radius(9).Fit(ui.Contain)
			ui.Row(c).FillWidth().Gap(8).Padding(0, 4, 2, 4).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, name).FontSize(sizeCaption).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, fmt.Sprintf("%d × %d", w, h)).FontSize(sizeCaption).TextColor(p.Secondary).FontFeatures("tnum").Shrink(0)
			})
		})
	}).AttachTo(thumb, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 8, 0).TransformOrigin(0, 1)
	peekIn(pop)
}

// removeButton is @lody/ui's mini secondary icon button in an
// attachment's corner: raised, tinting 4% under the pointer, sinking a
// pixel and flush while pressed, over 120ms.
func removeButton(c *ui.Context, p *palette, name string, remove func()) {
	b := ui.ButtonBase(c.Key("remove")).Size(24, 24).Radius(6).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Cursor(ui.CursorPointer).
		Label(locales.AttachRemove(name)).OnClick(remove).Absolute().Top(4).Right(4).Transition(colorFade)
	fill := raisedGround(p)
	if p.Dark {
		fill = p.Popover
	}
	if b.Hovered() {
		fill = fill.Mix(p.Text, 0.04)
	}
	if b.Pressed() {
		b.Background(fill).Shadow(0, 0, 0, 0.5, p.Text.Alpha(0.14)).InsetShadow(0, 1, 1, 0, p.Text.Alpha(0.14))
	} else {
		raisedLook(b, p, fill)
	}
	pressable(b, 0.97)
	b.Children(func() { ui.Icon(c, iconX).Size(12, 12).TextColor(p.Text) })
}

func byteSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// upload sends the attachments to Lody Cloud for a session, reporting
// progress on the main thread, and returns their blocks.
func (a *App) upload(ctx context.Context, workspace, sessionID string, files []pendingFile) ([]map[string]any, error) {
	var blocks []map[string]any
	for i, f := range files {
		path := f.Path
		progress := func(sent, total int64) {
			label := locales.Uploading()
			if len(files) > 1 {
				label = locales.UploadingCount(i+1, len(files))
			}
			var pct float32
			if total > 0 {
				label += fmt.Sprintf(" · %d%%", sent*100/total)
				pct = float32(sent) * 100 / float32(total)
			}
			a.update(func() {
				a.uploading = label
				if a.fileProgress != nil {
					a.fileProgress[path] = pct
				}
			})
		}
		progress(0, 0)
		block, err := a.api.UploadAttachment(ctx, a.tokenNow(), workspace, sessionID, f.Path, progress)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		blocks = append(blocks, block)
		a.update(func() {
			if a.fileProgress != nil {
				a.fileProgress[path] = 100
			}
		})
	}
	return blocks, nil
}

// sendingAttachments keeps a message's attachments in the composer while
// they upload, as Lody's: they take no edits then, and show how far each
// has gone.
func (a *App) sendingAttachments(files []pendingFile) {
	a.sendingFiles = files
	a.fileProgress = map[string]float32{}
}

func (a *App) sentAttachments() {
	a.sendingFiles, a.fileProgress = nil, nil
}

// urlImage fetches a picture by URL once and keeps it; Lody's own URLs get
// the session token.
func (a *App) urlImage(url string) *remoteImage {
	key := "url:" + url
	if img := a.images[key]; img != nil {
		return img
	}
	img := &remoteImage{loading: true}
	a.images[key] = img
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var bm *ui.Bitmap
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			if strings.HasPrefix(url, cloud.CloudOrigin+"/") || strings.HasPrefix(url, cloud.AuthOrigin+"/") {
				req.Header.Set("Authorization", "Bearer "+a.tokenNow())
			}
			var res *http.Response
			if res, err = http.DefaultClient.Do(req); err == nil {
				data, _ := io.ReadAll(io.LimitReader(res.Body, 10<<20))
				res.Body.Close()
				if res.StatusCode == http.StatusOK {
					bm, err = ui.DecodeBitmap(data)
				} else {
					err = fmt.Errorf("status %d", res.StatusCode)
				}
			}
		}
		a.update(func() { a.images[key] = &remoteImage{bitmap: bm, failed: err != nil} })
	}()
	return img
}

// sessionImage shows a picture of a session, fetched once and kept.
func (a *App) sessionImage(c *ui.Context, p *palette, sessionID string, it session.Item, size float32) {
	key := "img:" + it.ImageID
	img := a.images[key]
	if img == nil && a.conn != nil {
		img = &remoteImage{loading: true}
		a.images[key] = img
		workspace, storage := a.conn.ID, firstNonEmpty(it.StorageSessionID, sessionID)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			data, err := a.api.SessionImage(ctx, a.tokenNow(), workspace, storage, it.ImageID, 480)
			var bm *ui.Bitmap
			if err == nil {
				bm, err = ui.DecodeBitmap(data)
			}
			a.update(func() { a.images[key] = &remoteImage{bitmap: bm, failed: err != nil} })
		}()
	}
	ui.Box(c.Key(key)).Size(size, size).Radius(12).Clip().Background(p.Text.Alpha(0.05)).Label(firstNonEmpty(it.FileName, locales.Image())).Children(func() {
		switch {
		case img != nil && img.bitmap != nil:
			// Shown as it arrives, without a fade, as Lody's.
			ui.Image(c.Key("bitmap"), img.bitmap).Size(size, size).Fit(ui.Cover)
		case img != nil && img.loading:
			// Its room breathes while it comes, as @lody/ui's skeleton.
			skeleton(c, p, key, size).Width(size).Radius(0)
		default:
			ui.Box(c).Size(size, size).Center().Children(func() { ui.Icon(c, iconImage).Size(20, 20).TextColor(p.Secondary) })
		}
	})
}

// fileChip shows an attached file of a message.
func fileChip(c *ui.Context, p *palette, it session.Item) {
	ui.Row(c.Key(it.ID)).Height(40).Padding(0, 10).Radius(10).Gap(8).AlignItems(ui.Center).Background(p.UserBubble).MaxWidth(280).Children(func() {
		ui.Icon(c, iconFile).Size(16, 16).TextColor(p.Secondary)
		ui.Column(c).MinWidth(0).Children(func() {
			ui.Text(c, firstNonEmpty(it.FileName, locales.File())).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine()
			if it.SizeBytes > 0 {
				ui.Text(c, byteSize(it.SizeBytes)).FontSize(sizeCaption).TextColor(p.Secondary)
			}
		})
	})
}

// sendProgress is how much of the message going out has been sent, as a
// percentage of its files' bytes, and whether there are bytes to measure.
func (a *App) sendProgress() (float32, bool) {
	var total, sent float64
	for _, f := range a.sendingFiles {
		total += float64(f.Size)
		sent += float64(f.Size) * float64(min(100, max(0, a.fileProgress[f.Path]))) / 100
	}
	if total <= 0 {
		return 0, false
	}
	return float32(sent / total * 100), true
}
