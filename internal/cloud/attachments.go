package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoders for images that need re-encoding
	"image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// CloudOrigin serves attachments. Tests point it elsewhere.
var CloudOrigin = "https://api.lody.ai"

// multipartPart is the size of the parts of a large file's upload, and the
// size above which a file uploads in parts.
var multipartPart int64 = 16 << 20

// Limits of what a turn may carry, as Lody's clients enforce them.
const (
	MaxAttachments  = 16
	MaxImages       = 8
	MaxFiles        = 8
	MaxImageBytes   = 5 << 20
	MaxFileBytes    = 100 << 20
	imageMaxPixels  = 2048
	textPreviewScan = 8192
)

// IsImage reports whether a file uploads as an image: a picture, not a
// video.
func IsImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".heic", ".heif":
		return true
	}
	return false
}

// segment escapes a path segment or header value as the iOS client does:
// letters, digits, "-" and "_" stay.
func segment(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Progress reports bytes sent of a total.
type Progress func(sent, total int64)

type countingReader struct {
	r        io.Reader
	sent     int64
	total    int64
	progress Progress
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.sent += int64(n)
	if c.progress != nil && n > 0 {
		c.progress(c.sent, c.total)
	}
	return n, err
}

func (a *API) attachmentRequest(ctx context.Context, token, method, target string, header http.Header, body []byte, progress Progress, offset, total int64) (map[string]any, error) {
	var r io.Reader
	if body != nil {
		r = &countingReader{r: bytes.NewReader(body), total: total, sent: offset, progress: progress}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Transport: a.client().Transport}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("upload failed: %s", statusText(res.StatusCode, data))
	}
	out := map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, errors.New("upload failed: invalid response")
		}
	}
	return out, nil
}

// UploadAttachment uploads a file for a session and returns the block a
// turn's inputBlocks carry: {type: "image", imageId, ...} or {type: "file",
// fileId, ...}.
func (a *API) UploadAttachment(ctx context.Context, token, workspace, session, path string, progress Progress) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("%s is empty or not a file", filepath.Base(path))
	}
	if info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("%s is larger than 100 MB", filepath.Base(path))
	}
	kind := "file"
	if IsImage(path) {
		kind = "image"
	}
	base := CloudOrigin + "/api/workspaces/" + segment(workspace) + "/session-" + kind + "s"
	var result map[string]any
	if kind == "image" {
		result, err = a.uploadImage(ctx, token, base, session, path, progress)
	} else {
		result, err = a.uploadFile(ctx, token, base, session, path, info.Size(), progress)
	}
	if err != nil {
		return nil, err
	}
	block, _ := result[kind].(map[string]any)
	idKey := kind + "Id"
	id, _ := block[idKey].(string)
	size, _ := block["sizeBytes"].(float64)
	if block == nil || block["type"] != kind || id == "" || size <= 0 {
		return nil, errors.New("upload failed: invalid response")
	}
	if _, ok := block["mimeType"].(string); !ok {
		return nil, errors.New("upload failed: invalid response")
	}
	return block, nil
}

func (a *API) uploadImage(ctx context.Context, token, base, session, path string, progress Progress) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	switch contentType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		contentType = ""
	}
	if contentType == "" || len(data) > MaxImageBytes {
		// Re-encode what the server won't take as it is: JPEG, at most
		// 2048 pixels on its long side.
		data, err = reencode(data)
		if err != nil {
			return nil, fmt.Errorf("%s: can't read this image", name)
		}
		contentType = "image/jpeg"
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
	}
	if len(data) > MaxImageBytes {
		return nil, fmt.Errorf("%s is larger than 5 MB", name)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("sessionId", session)
	h := textproto.MIMEHeader{}
	safe := strings.NewReplacer(`"`, "_", "\r", "_", "\n", "_").Replace(name)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, safe))
	h.Set("Content-Type", contentType)
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, err
	}
	part.Write(data)
	w.Close()
	header := http.Header{}
	header.Set("Content-Type", w.FormDataContentType())
	return a.attachmentRequest(ctx, token, http.MethodPost, base+"/upload", header, body.Bytes(), progress, 0, int64(body.Len()))
}

func reencode(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	if w, h := b.Dx(), b.Dy(); w > imageMaxPixels || h > imageMaxPixels {
		scale := float64(imageMaxPixels) / float64(max(w, h))
		dst := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (a *API) uploadFile(ctx context.Context, token, base, session, path string, size int64, progress Progress) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return nil, err
	}
	prefix := make([]byte, textPreviewScan)
	n, _ := f.ReadAt(prefix, 0)
	prefix = prefix[:n]
	textPreview := !bytes.Contains(prefix, []byte{0}) && utf8.Valid(trimPartialRune(prefix))
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	contentType, _, _ = strings.Cut(contentType, ";")
	header := http.Header{}
	header.Set("x-session-id", session)
	header.Set("x-file-name", segment(filepath.Base(path)))
	header.Set("x-file-mime-type", contentType)
	header.Set("x-file-sha256", hex.EncodeToString(hash.Sum(nil)))
	header.Set("x-file-size-bytes", strconv.FormatInt(size, 10))
	header.Set("x-file-text-preview", strconv.FormatBool(textPreview))

	if size <= multipartPart {
		body, err := io.ReadAll(io.NewSectionReader(f, 0, size))
		if err != nil {
			return nil, err
		}
		return a.attachmentRequest(ctx, token, http.MethodPost, base+"/upload", header, body, progress, 0, size)
	}
	created, err := a.attachmentRequest(ctx, token, http.MethodPost, base+"/multipart/create", header, nil, nil, 0, 0)
	if err != nil {
		return nil, err
	}
	uploadID, _ := created["uploadId"].(string)
	fileID, _ := created["fileId"].(string)
	if uploadID == "" || fileID == "" {
		return nil, errors.New("upload failed: invalid response")
	}
	path2 := base + "/multipart/" + segment(uploadID)
	identity := http.Header{}
	identity.Set("x-session-id", session)
	identity.Set("x-file-id", fileID)
	abort := func() {
		a.attachmentRequest(context.Background(), token, http.MethodDelete, path2+"/abort", identity, nil, nil, 0, 0)
	}
	var parts []map[string]any
	buf := make([]byte, multipartPart)
	for offset := int64(0); offset < size; offset += multipartPart {
		n, err := f.ReadAt(buf, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			abort()
			return nil, err
		}
		h := identity.Clone()
		h.Set("x-file-part-size-bytes", strconv.Itoa(n))
		number := len(parts) + 1
		part, err := a.attachmentRequest(ctx, token, http.MethodPut, fmt.Sprintf("%s/part/%d", path2, number), h, buf[:n], progress, offset, size)
		if err != nil {
			abort()
			return nil, err
		}
		etag, _ := part["etag"].(string)
		if etag == "" {
			abort()
			return nil, errors.New("upload failed: invalid response")
		}
		parts = append(parts, map[string]any{"partNumber": number, "etag": etag})
	}
	body, _ := json.Marshal(map[string]any{"parts": parts})
	h := identity.Clone()
	h.Set("Content-Type", "application/json")
	result, err := a.attachmentRequest(ctx, token, http.MethodPost, path2+"/complete", h, body, nil, 0, 0)
	if err != nil {
		abort()
		return nil, err
	}
	return result, nil
}

// trimPartialRune drops a UTF-8 sequence cut at the end of a prefix.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size > 1 {
			return b
		}
		b = b[:len(b)-1]
	}
	return b
}

// SessionImage fetches a scaled-down copy of a session's image.
func (a *API) SessionImage(ctx context.Context, token, workspace, session, imageID string, width int) ([]byte, error) {
	target := fmt.Sprintf("%s/api/workspaces/%s/session-images/%s/%s/thumbnail?%s", CloudOrigin,
		segment(workspace), segment(session), segment(imageID),
		url.Values{"width": {strconv.Itoa(width)}, "fit": {"scale-down"}, "quality": {"85"}}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := a.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image unavailable (%d)", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 20<<20))
}

// SessionImageOriginal downloads an image a session holds, at its size,
// with its media type.
func (a *API) SessionImageOriginal(ctx context.Context, token, workspace, session, imageID string, limit int64) ([]byte, string, error) {
	target := fmt.Sprintf("%s/api/workspaces/%s/session-images/%s/%s", CloudOrigin, segment(workspace), segment(session), segment(imageID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image unavailable (%d)", res.StatusCode)
	}
	if res.ContentLength > limit {
		return nil, "", fmt.Errorf("image exceeds size limit")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > limit {
		return nil, "", fmt.Errorf("image exceeds size limit")
	}
	mediaType, _, _ := strings.Cut(res.Header.Get("Content-Type"), ";")
	if mediaType = strings.TrimSpace(mediaType); mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return b, mediaType, nil
}
