package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ConvexOrigin serves Lody's Convex functions: billing, usage, GitHub,
// sharing, and the machines and projects a workspace can see. Tests point
// it elsewhere.
var ConvexOrigin = "https://convex.lody.ai"

// Convex calls Lody's Convex functions as the signed-in user, with a JWT
// the backend mints from the sign-in token and that it keeps until it is
// about to expire.
type Convex struct {
	API   *API
	Token func() string // the sign-in token

	mu      sync.Mutex
	jwt     string
	expires time.Time
}

// ConvexError is an error a Convex function reports.
type ConvexError struct {
	Path    string
	Message string
}

func (e *ConvexError) Error() string { return "convex: " + e.Path + ": " + e.Message }

func (c *Convex) bearer(ctx context.Context, fresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !fresh && c.jwt != "" && time.Until(c.expires) > time.Minute {
		return c.jwt, nil
	}
	var res struct {
		Token string `json:"token"`
	}
	if err := c.API.call(ctx, c.Token(), "/api/auth/convex/token", nil, &res); err != nil {
		return "", err
	}
	if res.Token == "" {
		return "", errors.New("cloud: no Convex token")
	}
	c.jwt, c.expires = res.Token, jwtExpiry(res.Token)
	return c.jwt, nil
}

// jwtExpiry reads a JWT's exp, unverified: only to know when to ask again.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		if b, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			var claims struct {
				Exp float64 `json:"exp"`
			}
			if json.Unmarshal(b, &claims) == nil && claims.Exp > 0 {
				return time.Unix(int64(claims.Exp), 0)
			}
		}
	}
	return time.Now().Add(5 * time.Minute)
}

// Query runs a Convex query.
func (c *Convex) Query(ctx context.Context, path string, args, out any) error {
	return c.call(ctx, "query", path, args, out)
}

// Mutation runs a Convex mutation.
func (c *Convex) Mutation(ctx context.Context, path string, args, out any) error {
	return c.call(ctx, "mutation", path, args, out)
}

// Action runs a Convex action.
func (c *Convex) Action(ctx context.Context, path string, args, out any) error {
	return c.call(ctx, "action", path, args, out)
}

func (c *Convex) call(ctx context.Context, kind, path string, args, out any) error {
	if args == nil {
		args = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{"path": path, "format": "json", "args": args})
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		jwt, err := c.bearer(ctx, attempt > 0)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ConvexOrigin+"/api/"+kind, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		res, err := c.API.client().Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
		res.Body.Close()
		if err != nil {
			return err
		}
		if res.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue // the JWT went stale: mint another once
		}
		var reply struct {
			Status       string          `json:"status"`
			Value        json.RawMessage `json:"value"`
			ErrorMessage string          `json:"errorMessage"`
		}
		if err := json.Unmarshal(data, &reply); err != nil {
			return fmt.Errorf("convex: %s: status %d", path, res.StatusCode)
		}
		if reply.Status != "success" {
			msg := reply.ErrorMessage
			if msg == "" {
				msg = fmt.Sprintf("status %d", res.StatusCode)
			}
			if attempt == 0 && strings.Contains(strings.ToLower(msg), "unauthenticated") {
				continue
			}
			return &ConvexError{Path: path, Message: msg}
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(reply.Value, out)
	}
}
