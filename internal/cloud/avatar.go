package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"golang.org/x/image/draw"

	// The kinds of pictures Lody takes as avatars.
	_ "golang.org/x/image/webp"
	_ "image/gif"
)

// Avatars, of users and workspaces, are pictures Lody keeps at
// api.lody.ai: square, at most 512 pixels and 1 MB, as its uploader crops
// them.
const (
	avatarSize     = 512
	maxAvatarBytes = 1 << 20
)

// AvatarImage crops a picture to its centered square, at most 512 pixels,
// encoded small enough to upload.
func AvatarImage(data []byte) ([]byte, string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errors.New("can't read this image")
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	if side == 0 {
		return nil, "", errors.New("the image is empty")
	}
	min := image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2)
	crop := image.Rectangle{Min: min, Max: min.Add(image.Pt(side, side))}
	out := side
	if out > avatarSize {
		out = avatarSize
	}
	dst := image.NewRGBA(image.Rect(0, 0, out, out))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, crop, draw.Over, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err == nil && buf.Len() <= maxAvatarBytes {
		return buf.Bytes(), "image/png", nil
	}
	for _, q := range []int{90, 80, 70} {
		buf.Reset()
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: q}); err != nil {
			return nil, "", err
		}
		if buf.Len() <= maxAvatarBytes {
			return buf.Bytes(), "image/jpeg", nil
		}
	}
	return nil, "", errors.New("the image is too large")
}

// UploadAvatar uploads a picture for the user ("user") or the workspace
// ("workspace") and returns the URL to set as their image or logo.
func (a *API) UploadAvatar(ctx context.Context, token, workspace, kind string, data []byte, contentType string) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("kind", kind)
	h := textproto.MIMEHeader{}
	name := "avatar.png"
	if contentType == "image/jpeg" {
		name = "avatar.jpg"
	}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
	h.Set("Content-Type", contentType)
	part, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	part.Write(data)
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, CloudOrigin+"/api/workspaces/"+segment(workspace)+"/avatars/upload", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Avatar struct {
			AvatarID string `json:"avatarId"`
		} `json:"avatar"`
		Error string `json:"error"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode >= 300 || out.Avatar.AvatarID == "" {
		return "", fmt.Errorf("avatar upload: status %d %s", res.StatusCode, out.Error)
	}
	return CloudOrigin + "/api/avatars/" + segment(out.Avatar.AvatarID), nil
}
