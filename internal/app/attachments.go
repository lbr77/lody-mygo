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

// attachmentChips shows the composer's attachments, each removable.
func (a *App) attachmentChips(c *ui.Context, p *palette) {
	if len(a.attachments) == 0 {
		return
	}
	ui.Row(c).Wrap().Gap(8).Padding(2, 2, 6, 2).Children(func() {
		for i, f := range a.attachments {
			index := i
			remove := func() { a.attachments = append(a.attachments[:index:index], a.attachments[index+1:]...) }
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
					removeButton(c, p, f.Name, remove)
				})
				if img != nil && img.bitmap != nil {
					a.imagePeek(c, p, thumb, open, img.bitmap, f.Name)
				}
				continue
			}
			ui.Row(c.Key(f.Path)).Height(44).Padding(0, 28, 0, 10).Radius(10).Gap(8).AlignItems(ui.Center).Background(p.Text.Alpha(0.05)).MaxWidth(240).Children(func() {
				ui.Icon(c, iconFile).Size(18, 18).TextColor(p.Secondary)
				ui.Column(c).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, f.Name).FontSize(sizeFoot).FontWeight(500).TextColor(p.Text).SingleLine()
					ui.Text(c, byteSize(f.Size)).FontSize(sizeCaption).TextColor(p.Secondary)
				})
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
	ui.Popover(c, thumb, open, func() {
		ui.Column(c).Gap(6).Padding(6).Children(func() {
			ui.Image(c, bm).Size(float32(w)*scale, float32(h)*scale).Radius(9).Fit(ui.Contain)
			ui.Row(c).FillWidth().Gap(8).Padding(0, 4, 2, 4).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, name).FontSize(sizeCaption).TextColor(p.Text).SingleLine().Grow(1).MinWidth(0)
				ui.Text(c, fmt.Sprintf("%d × %d", w, h)).FontSize(sizeCaption).TextColor(p.Secondary).FontFeatures("tnum").Shrink(0)
			})
		})
	}).AttachTo(thumb, ui.AnchorTopStart, ui.AnchorBottomStart).Margin(0, 0, 8, 0).TransformOrigin(0, 1).
		Transition(ui.ElementTransition{Animation: &peekSpring, Enter: &ui.Motion{Scale: 0.35}, Exit: &ui.Motion{Scale: 0.92}})
}

func removeButton(c *ui.Context, p *palette, name string, remove func()) {
	b := ui.ButtonBase(c).Size(18, 18).Radius(9).Background(p.Text.Alpha(0.7)).Cursor(ui.CursorPointer).
		Label(locales.AttachRemove(name)).OnClick(remove).Absolute().Top(4).Right(4)
	b.Children(func() { ui.Icon(c, iconX).Size(10, 10).TextColor(p.Canvas) })
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
		progress := func(sent, total int64) {
			label := locales.Uploading()
			if len(files) > 1 {
				label = locales.UploadingCount(i+1, len(files))
			}
			if total > 0 {
				label += fmt.Sprintf(" · %d%%", sent*100/total)
			}
			a.update(func() { a.uploading = label })
		}
		progress(0, 0)
		block, err := a.api.UploadAttachment(ctx, a.tokenNow(), workspace, sessionID, f.Path, progress)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
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
			// An image fades in as it arrives, over 180ms.
			ui.Image(c.Key("bitmap"), img.bitmap).Size(size, size).Fit(ui.Cover).
				Transition(ui.ElementTransition{Duration: durRegular, Ease: easeStandard, Enter: &ui.Motion{}})
		case img != nil && img.loading:
			ui.Box(c).Size(size, size).Center().Children(func() { spinner(c).Size(14, 14) })
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
