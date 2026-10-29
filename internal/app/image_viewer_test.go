package app

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/collab"
	"github.com/lbr77/lody-mygo/internal/session"
	"github.com/lbr77/lody-mygo/locales"
)

var (
	red   = color.RGBA{220, 40, 40, 255}
	green = color.RGBA{40, 200, 60, 255}
	blue  = color.RGBA{40, 80, 230, 255}
)

// viewerPicture is a w×h picture of one color, as the viewer has one: its
// bitmap, and its bytes as a PNG.
func viewerPicture(w, h int, col color.RGBA) *remoteImage {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{col}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return &remoteImage{bitmap: ui.NewBitmap(img), data: buf.Bytes(), mime: "image/png"}
}

// stillFixture is the fixture with nothing moving behind the viewer, whose
// every frame draws its mask anew: the agent at rest.
func stillFixture() *App {
	a := fixture()
	a.view.Working = false
	a.catalog.Sessions[0].Status = "idle"
	return a
}

// viewerFixture is the open conversation with pictures on its last
// message, each of whose thumbnail and original are in hand, as w×h
// originals in turn red, green and blue.
func viewerFixture(w, h int, names ...string) *App {
	a := stillFixture()
	a.view.Entries = a.view.Entries[:3]
	var pics []session.Item
	for i, name := range names {
		id := string(rune('a'+i)) + "img"
		pics = append(pics, session.Item{ID: "g/" + id, Type: "image", ImageID: id, FileName: name})
		a.images["img:"+id] = viewerPicture(64, 48, []color.RGBA{red, green, blue}[i%3])
		a.images["orig:"+id] = viewerPicture(w, h, []color.RGBA{red, green, blue}[i%3])
	}
	a.view.Entries[2].Items = append(a.view.Entries[2].Items, session.Item{ID: "g", Type: "image_group", Images: pics})
	return a
}

// The mask's shader runs on the CPU in tests, every pixel of every frame:
// only the test that looks at it draws it.
func init() { viewerShaderMask = false }

// viewerTester is a w×h window of a with the conversation's first picture
// open in the viewer, which has come in.
func viewerTester(a *App, w, h int) *ui.Tester {
	tt := ui.NewTester(a.render, w, h)
	tt.SetTitleBar(ui.TitleBar{Height: 44, Left: 80})
	tt.Frame()
	a.openSessionGallery("s1", galleryKey("u2", 1, 0, "aimg"))
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	return tt
}

func pixelAt(img *image.RGBA, x, y int) color.RGBA {
	o := img.PixOffset(x, y)
	return color.RGBA{img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3]}
}

func nearRGBA(a, b color.RGBA) bool {
	d := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
	return d(a.R, b.R) < 12 && d(a.G, b.G) < 12 && d(a.B, b.B) < 12
}

func approx(a, b float32) bool { return math.Abs(float64(a-b)) < 0.01 }

// A message's picture opens the viewer over the app: the picture grows out
// of the middle as it fades in, over the window darkening and blurring
// over 400ms, under the bar with the count and the close button, which has
// the focus, between the previous and next buttons.
func TestImageViewerOpens(t *testing.T) {
	viewerShaderMask = true
	defer func() { viewerShaderMask = false }()
	a := viewerFixture(800, 600, "one.png", "two.png", "three.png")
	tt := snap(t, a, "viewer-closed", false, nil)
	if err := tt.Click("one.png"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	v := &a.viewer
	if !v.open || v.key != galleryKey("u2", 1, 0, "aimg") || !tt.HasText("1 / 3") {
		t.Fatalf("open=%v key=%q %q", v.open, v.key, tt.Texts())
	}
	ph := v.photos[v.key]
	if ph == nil || !ph.opens || ph.w != 800 || ph.h != 600 {
		t.Fatalf("the picture: %+v", ph)
	}
	// A quarter of the way: smaller than its inset fit, fading in, the
	// mask lighter than it ends.
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	mid := tt.Image()
	edge, mask := pixelAt(mid, 850, 360), pixelAt(mid, 20, 400)
	if nearRGBA(edge, red) || nearRGBA(pixelAt(mid, 550, 360), red) {
		t.Errorf("the picture is in already: edge %v middle %v", edge, pixelAt(mid, 550, 360))
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	snapFrame(t, tt, "viewer-open")
	img := tt.Image()
	// At 92% of its fit: 736 wide about the middle.
	if !nearRGBA(pixelAt(img, 550, 360), red) || !nearRGBA(pixelAt(img, 850, 360), red) || nearRGBA(pixelAt(img, 925, 360), red) {
		t.Errorf("the picture: middle %v, 850 %v, 925 %v", pixelAt(img, 550, 360), pixelAt(img, 850, 360), pixelAt(img, 925, 360))
	}
	if m := pixelAt(img, 20, 400); m.R > 60 || m.G > 60 || m.B > 60 || m.R >= mask.R {
		t.Errorf("the mask: %v, after %v", m, mask)
	}
	// The bar centers on the traffic lights' line, clear of them.
	if r, ok := tt.Find(locales.ImagePreviewClose()); !ok || r.X != 1100-12-32 || r.Y != 7 || r.W != 32 {
		t.Errorf("close button at %+v", r)
	}
	if r, ok := tt.Find("1 / 3"); !ok || r.X != 80 {
		t.Errorf("count at %+v", r)
	}
	if r, ok := tt.Find(locales.ImagePreviewPrevious()); !ok || r.X != 8 || r.Y != 360-16 {
		t.Errorf("previous at %+v", r)
	}
	if r, ok := tt.Find(locales.ImagePreviewNext()); !ok || r.X != 1100-8-32 {
		t.Errorf("next at %+v", r)
	}
	if !tt.Focused(locales.ImagePreviewClose()) {
		t.Error("the close button hasn't the focus")
	}
}

// The arrow keys and the side buttons step through the turn's pictures,
// stopping at the ends of three; the picture swaps at once.
func TestImageViewerSteps(t *testing.T) {
	a := viewerFixture(400, 300, "one.png", "two.png", "three.png")
	tt := viewerTester(a, 640, 400)
	step := func(how func(), want string, col color.RGBA) {
		t.Helper()
		how()
		tt.Frame()
		if !tt.HasText(want) || !nearRGBA(pixelAt(tt.Image(), 320, 200), col) {
			t.Fatalf("want %q in %v, got %q, %v", want, col, tt.Texts(), pixelAt(tt.Image(), 320, 200))
		}
		// The next one along, which came on the strip, comes in meanwhile.
		tt.Advance(400 * time.Millisecond)
	}
	key := func(k ui.Key) func() { return func() { tt.Key(0, k) } }
	click := func(label string) func() {
		return func() {
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
		}
	}
	step(key(ui.KeyLeft), "1 / 3", red)
	step(key(ui.KeyRight), "2 / 3", green)
	step(click(locales.ImagePreviewNext()), "3 / 3", blue)
	step(key(ui.KeyRight), "3 / 3", blue)
	step(click(locales.ImagePreviewNext()), "3 / 3", blue)
	step(click(locales.ImagePreviewPrevious()), "2 / 3", green)
	if a.viewer.photos[a.viewer.key].opens && a.viewer.photos[a.viewer.key].ready == tt.Now() {
		t.Error("the picture came in anew")
	}
}

// Past three pictures the ends go round, and each picture stepped to comes
// in afresh, as react-photo-view mounts it again. The gallery is the
// turn's: the user's message's and its replies', not another turn's.
func TestImageViewerLoops(t *testing.T) {
	a := viewerFixture(400, 300, "one.png", "two.png", "three.png")
	a.view.Entries = append(a.view.Entries, session.Entry{ID: "r2", Role: "assistant", UserTurnID: "u2", Items: []session.Item{
		{ID: "x", Type: "text", Text: "Here is the chart."},
		{ID: "p", Type: "image", ImageID: "dimg", FileName: "chart.png"},
	}})
	a.view.Entries[0].Items = append(a.view.Entries[0].Items, session.Item{ID: "p", Type: "image", ImageID: "zimg", FileName: "other.png"})
	a.images["orig:dimg"] = viewerPicture(400, 300, green)
	tt := viewerTester(a, 640, 400)
	if !tt.HasText("1 / 4") {
		t.Fatalf("the turn's gallery: %q", tt.Texts())
	}
	if r, ok := tt.Find(locales.ImagePreviewPrevious()); !ok {
		t.Fatal("no previous")
	} else {
		tt.ClickAt(r.X+16, r.Y+16)
	}
	tt.Frame()
	v := &a.viewer
	if !tt.HasText("4 / 4") || v.key != galleryKey("r2", 1, 0, "dimg") {
		t.Fatalf("round the start: %q", tt.Texts())
	}
	if ph := v.photos[v.key]; ph == nil || !ph.opens || ph.ready != tt.Now() {
		t.Fatalf("it doesn't come in afresh: %+v", ph)
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if !tt.HasText("1 / 4") {
		t.Fatalf("round the end: %q", tt.Texts())
	}
}

// The wheel zooms by a 200th for each DIP, a pinch as Chromium hands it
// over, both about the pointer and over the picture alone, on the 400ms
// transition, from the fit to six times it.
func TestImageViewerZoom(t *testing.T) {
	a := viewerFixture(400, 300, "one.png")
	tt := viewerTester(a, 640, 400)
	v := &a.viewer
	ph := v.photos[v.key]
	if tt.HasText("1 / 1") {
		t.Error("the count shows for one picture")
	}
	if _, ok := tt.Find(locales.ImagePreviewNext()); ok {
		t.Error("next shows for one picture")
	}
	// Off the picture, nothing.
	tt.Wheel(20, 200, 0, -100)
	if ph.s.rest != 1 {
		t.Fatalf("zoomed off the picture: %v", ph.s.rest)
	}
	tt.Wheel(320, 200, 0, -100)
	tt.Frame()
	if ph.s.rest != 1.5 || ph.s.at(tt.Now()) != 1 {
		t.Fatalf("zoom %v, showing %v", ph.s.rest, ph.s.at(tt.Now()))
	}
	tt.Advance(400 * time.Millisecond)
	if ph.s.at(tt.Now()) != 1.5 {
		t.Fatalf("after 400ms: %v", ph.s.at(tt.Now()))
	}
	// About the pointer: what is under it stays.
	tt.Wheel(420, 200, 0, -100)
	if ph.s.rest != 2 || !approx(ph.x.rest, -100.0/3) {
		t.Fatalf("zoom %v at %v", ph.s.rest, ph.x.rest)
	}
	tt.Pinch(320, 200, float32(math.E))
	if ph.s.rest != 2.5 || !approx(ph.x.rest, -125.0/3) {
		t.Fatalf("pinched to %v at %v", ph.s.rest, ph.x.rest)
	}
	tt.Wheel(320, 200, 0, -100000)
	if ph.s.rest != 6 {
		t.Fatalf("most: %v", ph.s.rest)
	}
	tt.Wheel(320, 200, 0, 100000)
	tt.Advance(400 * time.Millisecond)
	if ph.s.rest != 1 || ph.x.rest != 0 || ph.y.rest != 0 {
		t.Fatalf("least: %v at %v, %v", ph.s.rest, ph.x.rest, ph.y.rest)
	}
	// A double click zooms to twice the fit, about the pointer, and back.
	tt.ClickAt(320, 200)
	tt.Advance(100 * time.Millisecond)
	tt.ClickAt(320, 200)
	if ph.s.rest != 2 {
		t.Fatalf("double click: %v", ph.s.rest)
	}
	tt.Advance(400 * time.Millisecond)
	tt.ClickAt(320, 200)
	tt.Advance(100 * time.Millisecond)
	tt.ClickAt(320, 200)
	if ph.s.rest != 1 {
		t.Fatalf("double click back: %v", ph.s.rest)
	}
	// Two clicks further apart are none.
	tt.Advance(400 * time.Millisecond)
	tt.ClickAt(320, 200)
	tt.Advance(300 * time.Millisecond)
	tt.ClickAt(320, 200)
	if ph.s.rest != 1 || !v.open {
		t.Fatalf("slow clicks: %v", ph.s.rest)
	}
}

// A picture larger than six times its fit zooms as far as its own size,
// and a double click goes there.
func TestImageViewerZoomLimit(t *testing.T) {
	a := viewerFixture(7000, 300, "wide.png")
	tt := viewerTester(a, 640, 400)
	ph := a.viewer.photos[a.viewer.key]
	natural := float32(7000) / 640
	if ph.w != 640 || !approx(ph.h, 300*640/7000.0) {
		t.Fatalf("fit %v×%v", ph.w, ph.h)
	}
	tt.Wheel(320, 200, 0, -100000)
	if ph.s.rest != natural {
		t.Fatalf("most %v, want %v", ph.s.rest, natural)
	}
	tt.Wheel(320, 200, 0, 100000)
	tt.ClickAt(320, 200)
	tt.Advance(100 * time.Millisecond)
	tt.ClickAt(320, 200)
	if ph.s.rest != natural {
		t.Fatalf("double click to %v, want %v", ph.s.rest, natural)
	}
}

// Zoomed, the picture pans under the pointer, half as far past its edge,
// and eases back to the edge over 400ms as it is let go; flung, it glides
// on, slowing, to the far edge.
func TestImageViewerPan(t *testing.T) {
	a := viewerFixture(400, 300, "one.png")
	tt := viewerTester(a, 640, 400)
	ph := a.viewer.photos[a.viewer.key]
	tt.ClickAt(320, 200)
	tt.Advance(100 * time.Millisecond)
	tt.ClickAt(320, 200)
	tt.Advance(400 * time.Millisecond)
	// 800×600 in 640×400: its middle goes 80 across either way.
	tt.Press(320, 200)
	tt.Move(330, 200) // within 20 DIPs, no direction yet
	if ph.x.rest != 0 {
		t.Fatalf("moved before 20 DIPs: %v", ph.x.rest)
	}
	for _, x := range []float32{370, 410, 450} {
		tt.Move(x, 200)
	}
	if ph.x.rest != 100 { // 40, 80, and 20 for the 40 past the edge
		t.Fatalf("dragged to %v", ph.x.rest)
	}
	tt.Advance(250 * time.Millisecond)
	tt.Release(450, 200)
	if ph.x.rest != 80 || ph.x.at(tt.Now()) != 100 {
		t.Fatalf("let go: to %v from %v", ph.x.rest, ph.x.at(tt.Now()))
	}
	tt.Advance(400 * time.Millisecond)
	if ph.x.at(tt.Now()) != 80 || !a.viewer.open {
		t.Fatalf("settled at %v", ph.x.at(tt.Now()))
	}
	// Flung left within 200ms, it glides to the left edge.
	tt.Press(320, 200)
	tt.Move(310, 200)
	tt.Move(290, 200)
	tt.Move(250, 200)
	tt.Advance(40 * time.Millisecond)
	tt.Release(250, 200)
	from := ph.x.at(tt.Now())
	if ph.x.rest != -80 {
		t.Fatalf("flung to %v", ph.x.rest)
	}
	tt.Advance(50 * time.Millisecond)
	if x := ph.x.at(tt.Now()); x >= from {
		t.Fatalf("not gliding: %v from %v", x, from)
	}
	tt.Advance(1500 * time.Millisecond)
	if ph.x.at(tt.Now()) != -80 {
		t.Fatalf("glided to %v", ph.x.at(tt.Now()))
	}
}

// At its fit, a drag across moves the strip, which goes on to the next
// picture past 40 DIPs over 600ms, half as far past the first; one up or
// down lightens the mask, which darkens again as it is let go.
func TestImageViewerSwipe(t *testing.T) {
	a := viewerFixture(400, 300, "one.png", "two.png", "three.png")
	tt := viewerTester(a, 640, 400)
	v := &a.viewer
	drag := func(xs ...float32) {
		tt.Press(xs[0], 200)
		for _, x := range xs[1:] {
			tt.Move(x, 200)
		}
	}
	drag(320, 300, 280, 200)
	if v.strip.rest != -80 {
		t.Fatalf("strip at %v", v.strip.rest)
	}
	tt.Release(200, 200)
	tt.Frame()
	if !tt.HasText("2 / 3") || v.strip.rest != 0 || !approx(v.strip.at(tt.Now()), 640+20-80) {
		t.Fatalf("swiped: %q, strip %v from %v", tt.Texts(), v.strip.rest, v.strip.at(tt.Now()))
	}
	tt.Advance(300 * time.Millisecond)
	if s := v.strip.at(tt.Now()); s <= 0 || s >= 580 {
		t.Fatalf("half way: %v", s)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if v.strip.at(tt.Now()) != 0 || !nearRGBA(pixelAt(tt.Image(), 320, 200), green) {
		t.Fatalf("slid: %v %v", v.strip.at(tt.Now()), pixelAt(tt.Image(), 320, 200))
	}
	drag(320, 340, 360, 420)
	tt.Release(420, 200)
	tt.Advance(600 * time.Millisecond)
	if !tt.HasText("1 / 3") {
		t.Fatalf("back: %q", tt.Texts())
	}
	// Past the first, half as far, and back.
	drag(320, 340, 360, 460)
	if v.strip.rest != 50 {
		t.Fatalf("past the first: %v", v.strip.rest)
	}
	tt.Release(460, 200)
	tt.Advance(600 * time.Millisecond)
	if !tt.HasText("1 / 3") || v.strip.at(tt.Now()) != 0 {
		t.Fatalf("at the first: %q %v", tt.Texts(), v.strip.at(tt.Now()))
	}
	// Down, the mask lightens a 400th for each DIP.
	tt.Press(320, 200)
	tt.Move(320, 220)
	tt.Move(320, 240)
	tt.Move(320, 340)
	if !approx(v.mask.rest, 0.86-100.0/400) || a.viewer.photos[v.key].y.rest != 60 {
		t.Fatalf("mask %v, picture down %v", v.mask.rest, a.viewer.photos[v.key].y.rest)
	}
	tt.Advance(250 * time.Millisecond)
	tt.Release(320, 340)
	tt.Advance(400 * time.Millisecond)
	if !approx(v.mask.at(tt.Now()), 0.86) || a.viewer.photos[v.key].y.at(tt.Now()) != 0 || !v.open {
		t.Fatalf("let go: mask %v, picture %v", v.mask.at(tt.Now()), a.viewer.photos[v.key].y.at(tt.Now()))
	}
}

// Escape, the close button, and a click on the mask close the viewer at
// once; a click on the picture or the bar doesn't. The focus goes back to
// what opened it.
func TestImageViewerCloses(t *testing.T) {
	a := viewerFixture(800, 600, "one.png", "two.png")
	tt := snap(t, a, "viewer-closes", false, nil)
	open := func() {
		t.Helper()
		if err := tt.Click("one.png"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !a.viewer.open {
			t.Fatal("not open")
		}
	}
	open()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.viewer.open || tt.HasText("1 / 2") {
		t.Fatal("Escape left it open")
	}
	if !tt.Focused("one.png") {
		t.Error("the focus isn't back on the picture")
	}
	open()
	tt.ClickAt(550, 360) // the picture
	tt.ClickAt(400, 20)  // the bar
	tt.Advance(time.Second)
	if !a.viewer.open {
		t.Fatal("closed by the picture or the bar")
	}
	tt.ClickAt(80, 600) // the mask
	tt.Frame()
	if a.viewer.open {
		t.Fatal("the mask left it open")
	}
	open()
	if err := tt.Click(locales.ImagePreviewClose()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.viewer.open {
		t.Fatal("the close button left it open")
	}
	// It goes with the conversation.
	open()
	a.selected = "s2"
	tt.Frame()
	if a.viewer.open {
		t.Fatal("open over another conversation")
	}
}

// The picture's menu copies it, as a PNG, and saves it as it came, under
// its own name, saying so; a cancelled save says nothing.
func TestImageViewerMenu(t *testing.T) {
	a := viewerFixture(400, 300, "one.png")
	jpg := image.NewRGBA(image.Rect(0, 0, 40, 30))
	draw.Draw(jpg, jpg.Bounds(), &image.Uniform{red}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, jpg, nil); err != nil {
		t.Fatal(err)
	}
	orig := a.images["orig:aimg"]
	orig.data, orig.mime = buf.Bytes(), "image/jpeg"
	tt := viewerTester(a, 640, 400)
	var copied []byte
	oldClip := shareImageClipboard
	shareImageClipboard = func(b []byte) error { copied = b; return nil }
	defer func() { shareImageClipboard = oldClip }()
	tt.RightClickAt(320, 200)
	if menu := tt.Menu(); !slices.Equal(menu, []string{locales.ImagePreviewCopy(), locales.ImagePreviewSave()}) {
		t.Fatalf("menu %q", menu)
	}
	if err := tt.ChooseMenuItem(locales.ImagePreviewCopy()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, err := png.Decode(bytes.NewReader(copied)); err != nil {
		t.Fatalf("copied no PNG: %v", err)
	}
	if !tt.HasText(locales.ImagePreviewCopied()) {
		t.Fatalf("no toast: %q", tt.Texts())
	}
	// A clipboard that won't take it.
	shareImageClipboard = func([]byte) error { return errors.New("clipboard busy") }
	tt.RightClickAt(320, 200)
	if err := tt.ChooseMenuItem(locales.ImagePreviewCopy()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(locales.ImagePreviewCopyFailed()) || !tt.HasText("clipboard busy") {
		t.Fatalf("no failure: %q", tt.Texts())
	}

	wait := pumpUpdates(t, a)
	var asked string
	answer := ""
	oldSave := shareImageSaveDialog
	shareImageSaveDialog = func(_ *mygo.Window, name string) (string, error) { asked = name; return answer, nil }
	defer func() { shareImageSaveDialog = oldSave }()
	tt.RightClickAt(320, 200)
	if err := tt.ChooseMenuItem(locales.ImagePreviewSave()); err != nil {
		t.Fatal(err)
	}
	wait("the cancelled save", func() bool { return asked != "" })
	if asked != "one.png" {
		t.Fatalf("asked to save %q", asked)
	}
	answer = filepath.Join(t.TempDir(), "saved.jpg")
	tt.RightClickAt(320, 200)
	if err := tt.ChooseMenuItem(locales.ImagePreviewSave()); err != nil {
		t.Fatal(err)
	}
	wait("the save", func() bool { _, err := os.Stat(answer); return err == nil && len(a.toasts) > 0 })
	tt.Frame()
	if got, _ := os.ReadFile(answer); !bytes.Equal(got, buf.Bytes()) || !tt.HasText(locales.ImagePreviewSaved()) {
		t.Fatalf("saved %d bytes; %q", len(got), tt.Texts())
	}
}

// A queued message's thumbnail opens the viewer, which loads the picture
// at its size then, or shows the thumbnail where that fails; it goes as
// the message leaves the queue.
func TestImageViewerQueued(t *testing.T) {
	a := stillFixture()
	a.view.Entries[3].Items[1].Permission = nil
	q := queued("q1", "Look at this")
	q.Items = append(q.Items, session.Item{ID: "p", Type: "image", ImageID: "qimg", FileName: "draft.png"})
	a.view.Queue = []session.Entry{q}
	a.images["img:qimg"] = viewerPicture(64, 48, green)
	a.images["orig:qimg"] = viewerPicture(400, 300, red)
	tt := snap(t, a, "viewer-queued", false, nil)
	label := locales.MqPreviewImage("draft.png")
	if err := tt.Click(label); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	v := &a.viewer
	if !v.open || v.key != "qimg" || v.photos["qimg"].nw != 400 {
		t.Fatalf("open=%v key=%q", v.open, v.key)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	a.images["orig:qimg"] = &remoteImage{failed: true}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ph := v.photos["qimg"]; ph == nil || ph.nw != 64 {
		t.Fatalf("not the thumbnail: %+v", ph)
	}
	a.view.Queue = nil
	tt.Frame()
	tt.Frame()
	if v.open {
		t.Fatal("open after the message left the queue")
	}
}

// A file's image opens the viewer from the Files panel, and closes with
// the file.
func TestImageViewerFile(t *testing.T) {
	a := stillFixture()
	a.view.Entries = a.view.Entries[:2]
	a.conn = &cloud.Conn{ID: "w1"}
	a.panelOpen = true
	pic := viewerPicture(300, 200, blue)
	in := a.inspectFor("s1")
	in.dirs[""] = &dirListing{entries: []collab.DirEntry{{Name: "logo.png", Type: "file"}}}
	in.openPanel(tabFiles)
	in.file = "assets/logo.png"
	in.preview = &filePreview{path: "assets/logo.png", image: pic.bitmap, picture: pic, binary: true, mime: "image/png"}
	tt := snap(t, a, "viewer-file", false, nil)
	if err := tt.Click(locales.ImagePreviewZoom()); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	v := &a.viewer
	if !v.open || v.key != "assets/logo.png" || v.photos[v.key].nw != 300 {
		t.Fatalf("open=%v key=%q", v.open, v.key)
	}
	in.file = ""
	tt.Frame()
	if v.open {
		t.Fatal("open after the file closed")
	}
}

// A picture that takes longer than 250ms shows as it comes, without the
// open motion, and zooms at once until it is first let go.
func TestImageViewerSlowPicture(t *testing.T) {
	a := viewerFixture(400, 300, "one.png")
	slow := a.images["orig:aimg"]
	a.images["orig:aimg"] = &remoteImage{loading: true}
	tt := viewerTester(a, 640, 400)
	v := &a.viewer
	if ph := v.photos[v.key]; ph == nil || !ph.ready.IsZero() || nearRGBA(pixelAt(tt.Image(), 320, 200), red) {
		t.Fatalf("shows before it comes: %+v", ph)
	}
	a.images["orig:aimg"] = slow
	tt.Frame()
	ph := v.photos[v.key]
	if ph.ready.IsZero() || ph.opens || !nearRGBA(pixelAt(tt.Image(), 320, 200), red) {
		t.Fatalf("came in with motion: %+v", ph)
	}
	tt.Wheel(320, 200, 0, -100)
	if ph.s.at(tt.Now()) != 1.5 {
		t.Fatalf("zoomed on a transition: %v", ph.s.at(tt.Now()))
	}
	tt.ClickAt(320, 200) // let go
	tt.Wheel(320, 200, 0, -100)
	if ph.s.rest != 2 || ph.s.at(tt.Now()) != 1.5 {
		t.Fatalf("after a press: %v showing %v", ph.s.rest, ph.s.at(tt.Now()))
	}
}

func TestPhotoFit(t *testing.T) {
	for _, c := range []struct{ nw, nh, w, h, y float32 }{
		{400, 300, 400, 300, 0},            // at its size
		{3000, 1000, 1100, 1100.0 / 3, 0},  // wide: the width
		{400, 2000, 144, 720, 0},           // tall but narrow: the height
		{1200, 4000, 1100, 11000.0 / 3, 0}, // three times as tall: the width, from the top
		{2000, 1500, 960, 720, 0},          // the height
	} {
		w, h, y := photoFit(c.nw, c.nh, 1100, 720)
		want := c.y
		if c.nh/c.nw >= 3 && c.nw >= 1100 {
			want = (c.h - 720) / 2
		}
		if !approx(w, c.w) || !approx(h, c.h) || !approx(y, want) {
			t.Errorf("%v×%v: %v×%v at %v", c.nw, c.nh, w, h, y)
		}
	}
}

func TestExportFileName(t *testing.T) {
	for _, c := range []struct{ name, mime, want string }{
		{"photo", "image/jpeg", "photo.jpg"},
		{"shots/pic.jpeg", "image/png", "pic.jpeg"},
		{`C:\pics\a.webp`, "image/webp", "a.webp"},
		{"", "image/webp", "image.webp"},
		{"  ", "", "image.png"},
		{"notes", "", "notes"},
	} {
		if got := exportFileName(c.name, c.mime); got != c.want {
			t.Errorf("%q, %q: %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}

// The originals kept stay under Lody's 200MB, those shown least lately
// going first; thumbnails stay.
func TestOriginalsCap(t *testing.T) {
	a := fixture()
	a.images = map[string]*remoteImage{"img:t": {data: make([]byte, 10)}}
	now := time.Now()
	for i, k := range []string{"orig:a", "orig:b", "orig:c"} {
		a.images[k] = &remoteImage{data: make([]byte, 90<<20), used: now.Add(time.Duration(i) * time.Second)}
	}
	a.images["orig:a"].used = now.Add(time.Hour) // shown last
	a.trimOriginals()
	if a.images["orig:b"] != nil || a.images["orig:a"] == nil || a.images["orig:c"] == nil || a.images["img:t"] == nil {
		t.Fatalf("kept %v", keysOf(a.images))
	}
}

func keysOf(m map[string]*remoteImage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
