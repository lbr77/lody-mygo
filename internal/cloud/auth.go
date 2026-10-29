// Package cloud talks to Lody Cloud: Better Auth sign-in, workspaces, the
// short-lived Streams grants, and Machine RPC over streams.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthOrigin is Lody's backend: sign-in and the tokens it mints. Tests
// point it elsewhere.
var AuthOrigin = "https://backend.lody.ai"

const (
	// ClientID is the device-flow client Lody registers for its CLI. Native
	// clients without a registration of their own use it too.
	ClientID = "lody-cli"
)

// ErrUnauthorized means the session token expired or was revoked.
var ErrUnauthorized = errors.New("cloud: session expired")

// API calls the backend with a Better Auth session token.
type API struct {
	HTTP *http.Client
}

func (a *API) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// call sends a JSON request: POST when body is set, GET otherwise. Cookies
// are never used; the token travels as a Bearer header.
func (a *API) call(ctx context.Context, token, path string, body any, out any) error {
	var r io.Reader
	method := http.MethodGet
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, AuthOrigin+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	// The token endpoint answers pending and denied polls with 400 and a JSON
	// error, which the caller reads.
	if res.StatusCode >= 300 && path != "/api/auth/device/token" {
		return fmt.Errorf("cloud: %s: %s", path, statusText(res.StatusCode, data))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("cloud: %s: %w", path, err)
	}
	return nil
}

func statusText(code int, body []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		if e.Message != "" {
			return e.Message
		}
		if e.Error != "" {
			return e.Error
		}
	}
	return http.StatusText(code)
}

// DeviceCode is the start of a device-flow sign-in.
type DeviceCode struct {
	DeviceCode              string  `json:"device_code"`
	UserCode                string  `json:"user_code"`
	VerificationURI         string  `json:"verification_uri"`
	VerificationURIComplete string  `json:"verification_uri_complete"`
	ExpiresIn               float64 `json:"expires_in"`
	Interval                float64 `json:"interval"`
}

// BrowserURL is the page where the user approves the sign-in, always on
// lody.ai.
func (d *DeviceCode) BrowserURL() (string, error) {
	raw := d.VerificationURIComplete
	if raw == "" {
		raw = d.VerificationURI
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || (u.Host != "lody.ai" && u.Host != "backend.lody.ai") {
		return "", fmt.Errorf("cloud: unexpected verification URL %q", raw)
	}
	u.Host = "lody.ai"
	return u.String(), nil
}

// RequestDeviceCode starts a sign-in.
func (a *API) RequestDeviceCode(ctx context.Context) (*DeviceCode, error) {
	var d DeviceCode
	if err := a.call(ctx, "", "/api/auth/device/code", map[string]string{"client_id": ClientID}, &d); err != nil {
		return nil, err
	}
	if d.DeviceCode == "" || d.UserCode == "" || d.ExpiresIn <= 0 {
		return nil, errors.New("cloud: invalid device code response")
	}
	if d.Interval <= 0 {
		d.Interval = 5
	}
	return &d, nil
}

// Sign-in outcomes other than success.
var (
	ErrAccessDenied = errors.New("cloud: sign-in was denied")
	ErrCodeExpired  = errors.New("cloud: sign-in code expired")
)

// PollToken waits for the user to approve the sign-in and returns the
// session token.
func (a *API) PollToken(ctx context.Context, d *DeviceCode) (string, error) {
	interval := time.Duration(d.Interval * float64(time.Second))
	deadline := time.Now().Add(time.Duration(d.ExpiresIn * float64(time.Second)))
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		var res struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		err := a.call(ctx, "", "/api/auth/device/token", map[string]string{
			"client_id":   ClientID,
			"device_code": d.DeviceCode,
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		}, &res)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue // a network hiccup; keep polling until the code expires
		}
		switch {
		case res.AccessToken != "":
			return res.AccessToken, nil
		case res.Error == "authorization_pending":
		case res.Error == "slow_down":
			interval += 5 * time.Second
		case res.Error == "access_denied":
			return "", ErrAccessDenied
		case res.Error == "expired_token":
			return "", ErrCodeExpired
		default:
			return "", fmt.Errorf("cloud: sign-in failed: %s", res.Error)
		}
	}
	return "", ErrCodeExpired
}

// User is the signed-in account.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Image string `json:"image,omitempty"`
}

// Workspace is a Better Auth organization.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
	Logo string `json:"logo,omitempty"`
}

// Account is the user and their workspaces.
type Account struct {
	User       User        `json:"user"`
	Workspaces []Workspace `json:"workspaces"`
}

// Account validates token and loads the user and workspaces.
func (a *API) Account(ctx context.Context, token string) (*Account, error) {
	var session *struct {
		User *User `json:"user"`
	}
	if err := a.call(ctx, token, "/api/auth/get-session", nil, &session); err != nil {
		return nil, err
	}
	if session == nil || session.User == nil || session.User.ID == "" {
		return nil, ErrUnauthorized
	}
	u := *session.User
	if u.Name == "" {
		u.Name = u.Email
	}
	if !strings.HasPrefix(u.Image, "https://") {
		u.Image = ""
	}
	var orgs []Workspace
	if err := a.call(ctx, token, "/api/auth/organization/list", nil, &orgs); err != nil {
		return nil, err
	}
	ws := orgs[:0]
	for _, o := range orgs {
		if o.ID == "" {
			continue
		}
		if !strings.HasPrefix(o.Logo, "https://") {
			o.Logo = ""
		}
		ws = append(ws, o)
	}
	return &Account{User: u, Workspaces: ws}, nil
}

// SignOut revokes the session token.
func (a *API) SignOut(ctx context.Context, token string) error {
	return a.call(ctx, token, "/api/auth/sign-out", map[string]any{}, nil)
}

// PreviewToken asks the backend to sign a preview operation for the
// machine to check: intent is what to do, where, under the machine's
// nonce. The token is the user's, and the backend must say so.
func (a *API) PreviewToken(ctx context.Context, token, userID string, intent map[string]any) (string, error) {
	var res struct {
		RequestToken    string `json:"requestToken"`
		RequesterUserID string `json:"requesterUserId"`
	}
	if err := a.call(ctx, token, "/api/session-preview/request-token", intent, &res); err != nil {
		return "", err
	}
	if res.RequestToken == "" || res.RequesterUserID != userID {
		return "", errors.New("cloud: the backend didn't sign the preview request for this user")
	}
	return res.RequestToken, nil
}
