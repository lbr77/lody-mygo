// Package mcp lists the tools of a workspace's MCP server from this
// computer, as Lody's settings do: it starts the server's command, or
// connects to its URL, asks for its tools and hangs up.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Tool is a tool a server offers.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Server is how to reach an MCP server, as a workspace's connection
// describes it.
type Server struct {
	Transport string // stdio or http

	Command        string
	Args           []string
	Env            map[string]string
	EnvPassthrough []string // variables passed on from this computer

	URL         string
	BearerToken string
	Headers     map[string]string
}

// FromConnection reads a workspace's connection object.
func FromConnection(conn map[string]any) (Server, error) {
	s := Server{Transport: text(conn["transport"])}
	switch s.Transport {
	case "stdio":
		s.Command = strings.TrimSpace(text(conn["command"]))
		if s.Command == "" {
			return s, errors.New("mcp: the command is empty")
		}
		for _, a := range list(conn["args"]) {
			s.Args = append(s.Args, text(a))
		}
		s.Env = stringMap(conn["env"])
		for _, v := range list(conn["envPassthrough"]) {
			s.EnvPassthrough = append(s.EnvPassthrough, text(v))
		}
	case "http":
		s.URL = strings.TrimSpace(text(conn["url"]))
		if s.URL == "" {
			return s, errors.New("mcp: the URL is empty")
		}
		if t, ok := conn["bearerToken"].(string); ok {
			s.BearerToken = t
		}
		s.Headers = stringMap(conn["headers"])
	default:
		return s, fmt.Errorf("mcp: unknown transport %q", s.Transport)
	}
	return s, nil
}

// MissingError names a ${VAR} the environment doesn't set.
type MissingError struct{ Variable string }

func (e *MissingError) Error() string { return "mcp: ${" + e.Variable + "} is not set" }

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Resolve fills the ${VAR} references in s from env.
func (s Server) Resolve(env map[string]string) (Server, error) {
	var missing string
	fill := func(v string) string {
		return envRef.ReplaceAllStringFunc(v, func(ref string) string {
			name := ref[2 : len(ref)-1]
			if value, ok := env[name]; ok && value != "" {
				return value
			}
			if missing == "" {
				missing = name
			}
			return ref
		})
	}
	out := s
	out.Command, out.URL, out.BearerToken = fill(s.Command), fill(s.URL), fill(s.BearerToken)
	out.Args = make([]string, len(s.Args))
	for i, a := range s.Args {
		out.Args[i] = fill(a)
	}
	out.Env = map[string]string{}
	for k, v := range s.Env {
		out.Env[k] = fill(v)
	}
	for _, name := range s.EnvPassthrough {
		value := env[name]
		if value == "" && missing == "" {
			missing = name
		}
		if _, set := out.Env[name]; !set {
			out.Env[name] = value
		}
	}
	out.Headers = map[string]string{}
	for k, v := range s.Headers {
		out.Headers[k] = fill(v)
	}
	if missing != "" {
		return s, &MissingError{missing}
	}
	return out, nil
}

// session is a connection to a server speaking JSON-RPC.
type session interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params any) error
	close()
}

// RPCError is an error a server answered with.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("mcp: %s (%d)", e.Message, e.Code) }

const protocolVersion = "2025-06-18"

// The bounds Lody's discovery keeps to.
const (
	maxPages = 100
	maxTools = 2000
	timeout  = 30 * time.Second
)

// ListTools connects to s, with ${VAR} resolved from env, and lists its
// tools. env is the environment the server's command runs in as well.
func ListTools(ctx context.Context, s Server, env map[string]string) ([]Tool, error) {
	resolved, err := s.Resolve(env)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var sess session
	if resolved.Transport == "http" {
		sess, err = dialHTTP(resolved)
	} else {
		sess, err = startStdio(ctx, resolved, env)
	}
	if err != nil {
		return nil, err
	}
	defer sess.close()
	if _, err := sess.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "lody-settings", "version": "1"},
	}); err != nil {
		return nil, err
	}
	if err := sess.notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	var tools []Tool
	seen := map[string]int{}
	cursor := ""
	for page := 0; page < maxPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := sess.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("mcp: %w", err)
		}
		for _, t := range res.Tools {
			if i, ok := seen[t.Name]; ok {
				tools[i] = t
				continue
			}
			seen[t.Name] = len(tools)
			tools = append(tools, t)
		}
		if len(tools) > maxTools {
			return nil, errors.New("mcp: the server lists too many tools")
		}
		if res.NextCursor == "" {
			return tools, nil
		}
		cursor = res.NextCursor
	}
	return tools, nil
}

func text(v any) string {
	s, _ := v.(string)
	return s
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func stringMap(v any) map[string]string {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = text(v)
	}
	return out
}

// sortedKeys keeps what a server is started with stable.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
