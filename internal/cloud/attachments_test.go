package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeAPI records uploads as api.lody.ai receives them.
type fakeAPI struct {
	mu       sync.Mutex
	images   []string // content types of image parts
	headers  []http.Header
	parts    int
	complete bool
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := r.URL.Path
	reply := func(v any) { json.NewEncoder(w).Encode(v) }
	switch {
	case strings.HasSuffix(path, "/session-images/upload"):
		if err := r.ParseMultipartForm(10 << 20); err != nil || r.FormValue("sessionId") != "s1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, header, _ := r.FormFile("file")
		f.images = append(f.images, header.Header.Get("Content-Type"))
		reply(map[string]any{"image": map[string]any{"type": "image", "imageId": "img1", "mimeType": header.Header.Get("Content-Type"), "sizeBytes": header.Size}})
	case strings.HasSuffix(path, "/session-files/upload"):
		f.headers = append(f.headers, r.Header.Clone())
		body, _ := io.ReadAll(r.Body)
		reply(map[string]any{"file": map[string]any{"type": "file", "fileId": "f1", "mimeType": r.Header.Get("x-file-mime-type"), "sizeBytes": len(body)}})
	case strings.HasSuffix(path, "/multipart/create"):
		f.headers = append(f.headers, r.Header.Clone())
		reply(map[string]any{"uploadId": "up1", "fileId": "f2"})
	case strings.Contains(path, "/multipart/up1/part/"):
		f.parts++
		reply(map[string]any{"etag": "e"})
	case strings.HasSuffix(path, "/multipart/up1/complete"):
		f.complete = true
		reply(map[string]any{"file": map[string]any{"type": "file", "fileId": "f2", "mimeType": "application/octet-stream", "sizeBytes": 3000}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestUploadAttachments(t *testing.T) {
	fake := &fakeAPI{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	defer func(origin string, part int64) { CloudOrigin, multipartPart = origin, part }(CloudOrigin, multipartPart)
	CloudOrigin, multipartPart = srv.URL, 1024
	dir := t.TempDir()
	api := &API{}
	ctx := context.Background()

	// A PNG goes as it is.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	png.Encode(&buf, img)
	pngPath := filepath.Join(dir, "shot.png")
	os.WriteFile(pngPath, buf.Bytes(), 0o600)
	block, err := api.UploadAttachment(ctx, "tok", "ws", "s1", pngPath, nil)
	if err != nil || block["imageId"] != "img1" {
		t.Fatalf("image: %v %v", block, err)
	}
	// A BMP is re-encoded as JPEG.
	bmpPath := filepath.Join(dir, "scan.tiff")
	os.WriteFile(bmpPath, buf.Bytes(), 0o600) // PNG bytes behind another extension
	if _, err := api.UploadAttachment(ctx, "tok", "ws", "s1", bmpPath, nil); err != nil {
		t.Fatal(err)
	}
	if fake.images[0] != "image/png" || fake.images[1] != "image/jpeg" {
		t.Fatalf("image types = %v", fake.images)
	}

	// A small text file in one request, with its headers.
	notes := filepath.Join(dir, "notes 1.md")
	os.WriteFile(notes, []byte("# hello\n"), 0o600)
	var progress int64
	block, err = api.UploadAttachment(ctx, "tok", "ws", "s1", notes, func(sent, total int64) { progress = sent })
	if err != nil || block["fileId"] != "f1" || progress != 8 {
		t.Fatalf("file: %v %v progress %d", block, err, progress)
	}
	h := fake.headers[0]
	if h.Get("x-session-id") != "s1" || h.Get("x-file-name") != "notes%201%2Emd" || h.Get("x-file-size-bytes") != "8" ||
		h.Get("x-file-text-preview") != "true" || len(h.Get("x-file-sha256")) != 64 {
		t.Fatalf("headers = %v", h)
	}

	// A large file in parts.
	big := filepath.Join(dir, "data.bin")
	os.WriteFile(big, bytes.Repeat([]byte{0, 1, 2}, 1000), 0o600)
	block, err = api.UploadAttachment(ctx, "tok", "ws", "s1", big, nil)
	if err != nil || block["fileId"] != "f2" || fake.parts != 3 || !fake.complete {
		t.Fatalf("multipart: %v %v parts %d", block, err, fake.parts)
	}
	if fake.headers[1].Get("x-file-text-preview") != "false" {
		t.Fatal("binary file marked as text")
	}
}
