// Package streams is a client for Durable Streams, the HTTP log protocol of
// Lody Cloud's gateway, with its bootstrap extension.
package streams

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	connectTimeout = 15 * time.Second
	pollTimeout    = 35 * time.Second
)

// Client talks to one stream.
type Client struct {
	URL   string
	Token func(ctx context.Context) (string, error)
	HTTP  *http.Client
}

// StreamURL is the URL of a named stream under a gateway, as Lody builds it.
func StreamURL(gateway, name string) string {
	return strings.TrimSuffix(gateway, "/") + "/ds/lody/" + EncodeURIComponent(name)
}

// EncodeURIComponent escapes s as JavaScript's encodeURIComponent does.
func EncodeURIComponent(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Error is a failed request. Code follows @loro-dev/streams-client.
type Error struct {
	Status int
	Code   string
	Body   string
}

func (e *Error) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("streams: %s (%d): %s", e.Code, e.Status, e.Body)
	}
	return fmt.Sprintf("streams: %s (%d)", e.Code, e.Status)
}

// IsNotFound reports whether err is a 404 from the gateway.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsUnauthorized reports whether the token was rejected.
func IsUnauthorized(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden)
}

func codeFor(status int) string {
	switch status {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 409:
		return "conflict"
	case 410:
		return "gone"
	case 413:
		return "payload_too_large"
	case 429:
		return "rate_limited"
	}
	if status >= 500 {
		return "server_error"
	}
	return "http_error"
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) do(ctx context.Context, method, target string, header http.Header, body []byte, timeout time.Duration) (*http.Response, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.http().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, nil, err
	}
	if res.StatusCode >= 300 {
		text := string(data)
		if len(text) > 512 {
			text = text[:512]
		}
		return res, nil, &Error{Status: res.StatusCode, Code: codeFor(res.StatusCode), Body: strings.TrimSpace(text)}
	}
	return res, data, nil
}

func isTrue(h http.Header, name string) bool { return strings.EqualFold(h.Get(name), "true") }

// Bootstrap is a snapshot plus the updates retained after it.
type Bootstrap struct {
	SnapshotOffset string
	Snapshot       []byte // nil when the stream has no snapshot
	Updates        [][]byte
	NextOffset     string
	Cursor         string
	UpToDate       bool
}

// Bootstrap fetches the stream's snapshot and retained updates.
func (c *Client) Bootstrap(ctx context.Context) (*Bootstrap, error) {
	base, _, _ := strings.Cut(c.URL, "?")
	res, data, err := c.do(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/bootstrap", nil, nil, connectTimeout)
	if err != nil {
		return nil, err
	}
	_, params, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return nil, errors.New("streams: bootstrap response is not multipart/mixed")
	}
	parts := decodeMultipart(params["boundary"], data)
	if len(parts) == 0 {
		return nil, errors.New("streams: bootstrap response has no parts")
	}
	b := &Bootstrap{
		SnapshotOffset: res.Header.Get("Stream-Snapshot-Offset"),
		NextOffset:     res.Header.Get("Stream-Next-Offset"),
		Cursor:         res.Header.Get("Stream-Cursor"),
		UpToDate:       isTrue(res.Header, "Stream-Up-To-Date"),
	}
	if b.NextOffset == "" || b.SnapshotOffset == "" {
		return nil, errors.New("streams: bootstrap response is missing offsets")
	}
	if !(b.SnapshotOffset == "-1" && len(parts[0]) == 0) {
		b.Snapshot = parts[0]
	}
	b.Updates = parts[1:]
	return b, nil
}

// decodeMultipart splits a multipart/mixed body the way streams-client
// does: binary-safe, tolerant of missing CRLFs around delimiters.
func decodeMultipart(boundary string, data []byte) [][]byte {
	delim := []byte("--" + boundary)
	var parts [][]byte
	i := bytes.Index(data, delim)
	if i < 0 {
		return nil
	}
	pos := i + len(delim)
	if pos < len(data) && data[pos] == '\r' {
		pos++
	}
	if pos < len(data) && data[pos] == '\n' {
		pos++
	}
	for pos < len(data) {
		if bytes.HasPrefix(data[pos:], []byte("--")) {
			break
		}
		headerEnd := bytes.Index(data[pos:], []byte("\r\n\r\n"))
		if headerEnd < 0 {
			break
		}
		bodyStart := pos + headerEnd + 4
		next := bytes.Index(data[bodyStart:], delim)
		if next < 0 {
			parts = append(parts, data[bodyStart:])
			break
		}
		bodyEnd := bodyStart + next
		if bodyEnd-bodyStart >= 2 && data[bodyEnd-2] == '\r' && data[bodyEnd-1] == '\n' {
			bodyEnd -= 2
		}
		parts = append(parts, data[bodyStart:bodyEnd])
		pos = bodyStart + next + len(delim)
		if pos+1 < len(data) && data[pos] == '-' && data[pos+1] == '-' {
			break
		}
		if pos < len(data) && data[pos] == '\r' {
			pos++
		}
		if pos < len(data) && data[pos] == '\n' {
			pos++
		}
	}
	return parts
}

// Read is one read result. Payload is empty when nothing new arrived.
type Read struct {
	Payload    []byte
	NextOffset string
	Cursor     string
	UpToDate   bool
	Closed     bool
}

// Read reads from offset. With live set, it long-polls when caught up.
func (c *Client) Read(ctx context.Context, offset, cursor string, live bool) (*Read, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("offset", offset)
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	timeout := connectTimeout
	if live {
		q.Set("live", "long-poll")
		timeout = pollTimeout + 5*time.Second
	}
	u.RawQuery = q.Encode()
	res, data, err := c.do(ctx, http.MethodGet, u.String(), nil, nil, timeout)
	if err != nil {
		return nil, err
	}
	r := &Read{
		NextOffset: res.Header.Get("Stream-Next-Offset"),
		Cursor:     res.Header.Get("Stream-Cursor"),
		UpToDate:   isTrue(res.Header, "Stream-Up-To-Date"),
		Closed:     isTrue(res.Header, "Stream-Closed"),
	}
	if r.NextOffset == "" {
		return nil, errors.New("streams: read response is missing Stream-Next-Offset")
	}
	if res.StatusCode == http.StatusNoContent {
		r.UpToDate = true
		return r, nil
	}
	r.Payload = data
	return r, nil
}

// Append writes one message and returns the new tail offset.
func (c *Client) Append(ctx context.Context, contentType string, body []byte) (string, error) {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	res, _, err := c.do(ctx, http.MethodPost, c.URL, h, body, connectTimeout)
	if err != nil {
		return "", err
	}
	return res.Header.Get("Stream-Next-Offset"), nil
}

// Create creates the stream, or succeeds when it exists with the same
// configuration. A ttl of zero means no expiry.
func (c *Client) Create(ctx context.Context, contentType string, ttl time.Duration) error {
	h := http.Header{}
	h.Set("Content-Type", contentType)
	if ttl > 0 {
		h.Set("Stream-TTL", strconv.Itoa(int(ttl/time.Second)))
	}
	_, _, err := c.do(ctx, http.MethodPut, c.URL, h, nil, connectTimeout)
	return err
}

// EncodeFrame prefixes b with its length, as a uint32 big-endian.
func EncodeFrame(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

// DecodeFrames splits concatenated frames. A body starting with '{' is one
// unframed JSON message.
func DecodeFrames(data []byte) ([][]byte, error) {
	if len(data) > 0 && data[0] == '{' {
		return [][]byte{data}, nil
	}
	var out [][]byte
	for off := 0; off < len(data); {
		if off+4 > len(data) {
			return nil, errors.New("streams: truncated frame length")
		}
		n := int(binary.BigEndian.Uint32(data[off:]))
		off += 4
		if off+n > len(data) {
			return nil, errors.New("streams: truncated frame body")
		}
		if n > 0 {
			out = append(out, data[off:off+n])
		}
		off += n
	}
	return out, nil
}

var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

var decoder, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))

// Unpack decompresses zstd snapshots and returns other bytes as they are.
func Unpack(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, zstdMagic) {
		return b, nil
	}
	return decoder.DecodeAll(b, nil)
}
