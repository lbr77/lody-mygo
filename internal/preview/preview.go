// Package preview shows a web server a session runs on its machine, as
// Lody does: the machine opens a Cloudflare quick tunnel to it, whose URL
// carries its own access token. The machine opens one only for the
// session's owner, on a proof the backend signs for this request.
package preview

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/lbr77/lody-mygo/internal/cloud"
	"github.com/lbr77/lody-mygo/internal/session"
)

const tokenParam = "__lody_preview_token"

var tunnelHost = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*\.trycloudflare\.com$`)

// ViewerURL is the page to load for a tunnel's public URL: the target's
// path on the tunnel, with the tunnel's token. It refuses URLs that aren't
// a quick tunnel's, as Lody's clients do.
func ViewerURL(publicURL, path string) (string, bool) {
	gateway, err := url.Parse(publicURL)
	if err != nil || gateway.Scheme != "https" || gateway.User != nil || gateway.Port() != "" ||
		!tunnelHost.MatchString(gateway.Hostname()) || gateway.Query().Get(tokenParam) == "" {
		return "", false
	}
	if path == "" {
		path = "/"
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", false
	}
	origin := &url.URL{Scheme: gateway.Scheme, Host: gateway.Host, Path: "/"}
	viewer := origin.ResolveReference(ref)
	// A path of //host would land elsewhere.
	if viewer.Scheme != gateway.Scheme || viewer.Host != gateway.Host {
		return "", false
	}
	q := viewer.Query()
	for name, values := range gateway.Query() {
		q[name] = values
	}
	viewer.RawQuery = q.Encode()
	viewer.Fragment = ""
	return viewer.String(), true
}

// Request is who asks to show what.
type Request struct {
	Workspace string
	Machine   string
	Session   string
	User      string
	Token     string // the user's sign-in token, for the backend's proof
}

// ErrUnsupported means the machine's Lody is too old to show previews.
var ErrUnsupported = errors.New("this machine's Lody can't share previews; update it")

// proof gets the machine's nonce and the backend's signature over the
// operation, which the machine checks with the backend.
func proof(ctx context.Context, conn *cloud.Conn, api *cloud.API, r Request, operation map[string]any) (map[string]any, error) {
	raw, err := conn.MachineRPC(ctx, r.Machine, "machine/preview-control", map[string]any{}, 15*time.Second)
	if err != nil {
		var rpcErr *cloud.RPCError
		if errors.As(err, &rpcErr) {
			return nil, err
		}
		// Older machines drop the method without a reply.
		return nil, ErrUnsupported
	}
	var control struct {
		RuntimeNonce string `json:"runtimeNonce"`
	}
	if json.Unmarshal(raw, &control) != nil || control.RuntimeNonce == "" {
		return nil, ErrUnsupported
	}
	requestID := uuid.NewString()
	token, err := api.PreviewToken(ctx, r.Token, r.User, map[string]any{
		"workspaceId": r.Workspace, "machineId": r.Machine, "sessionId": r.Session,
		"runtimeNonce": control.RuntimeNonce, "requestId": requestID, "operation": operation,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"runtimeNonce": control.RuntimeNonce, "requestId": requestID, "requestToken": token}, nil
}

type result struct {
	Success    bool   `json:"success"`
	Error      string `json:"error"`
	Message    string `json:"message"`
	Connection *struct {
		Status    string `json:"status"`
		PublicURL string `json:"publicUrl"`
	} `json:"connection"`
}

func (r result) err() error {
	if r.Message != "" {
		return errors.New(r.Message)
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return errors.New("the machine couldn't share the preview")
}

// Start opens a tunnel to target and returns the page to load. It can
// take minutes as the machine starts the tunnel.
func Start(ctx context.Context, conn *cloud.Conn, api *cloud.API, r Request, target session.PreviewTarget) (string, error) {
	p, err := proof(ctx, conn, api, r, map[string]any{"action": "create", "target": target, "restart": false})
	if err != nil {
		return "", err
	}
	raw, err := conn.MachineRPC(ctx, r.Machine, "session/preview-create", map[string]any{
		"sessionId": r.Session, "requestedByUserId": r.User, "proof": p, "target": target,
		"approval": map[string]any{
			"source": "browser_address", "targetClass": "loopback", "target": target,
			"confirmedByUserId": r.User, "confirmedAt": time.Now().UnixMilli(),
		},
	}, 290*time.Second)
	if err != nil {
		return "", err
	}
	var res result
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	if !res.Success || res.Connection == nil {
		return "", res.err()
	}
	viewer, ok := ViewerURL(res.Connection.PublicURL, target.Path)
	if !ok {
		return "", errors.New("the machine returned an unexpected preview address")
	}
	return viewer, nil
}

// Stop closes the session's tunnel.
func Stop(ctx context.Context, conn *cloud.Conn, api *cloud.API, r Request) error {
	p, err := proof(ctx, conn, api, r, map[string]any{"action": "revoke"})
	if err != nil {
		return err
	}
	raw, err := conn.MachineRPC(ctx, r.Machine, "session/preview-revoke", map[string]any{
		"sessionId": r.Session, "requestedByUserId": r.User, "proof": p,
	}, 35*time.Second)
	if err != nil {
		return err
	}
	var res result
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if !res.Success {
		return res.err()
	}
	return nil
}

// Connection is the session's preview as the machine reports it.
type Connection struct {
	Status       string `json:"status"` // creating, active, closed, failed
	EndpointID   string `json:"endpointId"`
	PublicURL    string `json:"publicUrl"`
	ClosedReason string `json:"closedReason"`
}

// Status asks the machine how the session's preview is, renewing the
// endpoint named, as the open page's heartbeat does.
func Status(ctx context.Context, conn *cloud.Conn, api *cloud.API, r Request, renewEndpointID string) (*Connection, error) {
	op := map[string]any{"action": "status"}
	params := map[string]any{"sessionId": r.Session, "requestedByUserId": r.User}
	if renewEndpointID != "" {
		op["renewEndpointId"], params["renewEndpointId"] = renewEndpointID, renewEndpointID
	}
	p, err := proof(ctx, conn, api, r, op)
	if err != nil {
		return nil, err
	}
	params["proof"] = p
	raw, err := conn.MachineRPC(ctx, r.Machine, "session/preview-status", params, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return statusOf(raw)
}

func statusOf(raw json.RawMessage) (*Connection, error) {
	var res struct {
		result
		Connection *Connection `json:"connection"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if !res.Success {
		return nil, res.err()
	}
	if res.Connection == nil {
		return &Connection{}, nil
	}
	return res.Connection, nil
}

// Endpoint is a direct preview on this machine: the page through the
// local Lody's proxy, without a tunnel.
type Endpoint struct {
	EndpointID string `json:"endpointId"`
	Kind       string `json:"kind"`
	ViewerURL  string `json:"viewerUrl"`
}

// LocalRPC sends a method to the Lody daemon on this machine.
type LocalRPC func(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error)

// Acquire opens a direct preview of target on this machine.
func Acquire(ctx context.Context, rpc LocalRPC, r Request, target session.PreviewTarget) (*Endpoint, error) {
	raw, err := rpc(ctx, "session/preview-endpoint-acquire", map[string]any{"sessionId": r.Session, "requestedByUserId": r.User, "target": target}, 10*time.Second)
	if err != nil {
		return nil, err
	}
	var res struct {
		result
		Endpoint *Endpoint `json:"endpoint"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if !res.Success || res.Endpoint == nil {
		return nil, res.err()
	}
	u, err := url.Parse(res.Endpoint.ViewerURL)
	if err != nil || res.Endpoint.Kind != "local-proxy" || u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
		Release(ctx, rpc, r, res.Endpoint.EndpointID)
		return nil, errors.New("the local preview endpoint is not one this app can show")
	}
	return res.Endpoint, nil
}

// Release closes a direct preview.
func Release(ctx context.Context, rpc LocalRPC, r Request, endpointID string) error {
	if endpointID == "" {
		return nil
	}
	raw, err := rpc(ctx, "session/preview-endpoint-release", map[string]any{"sessionId": r.Session, "endpointId": endpointID}, 5*time.Second)
	if err != nil {
		return err
	}
	var res result
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if !res.Success {
		return res.err()
	}
	return nil
}

// LocalStatus asks this machine's Lody how the session's preview is.
func LocalStatus(ctx context.Context, rpc LocalRPC, r Request, renewEndpointID string) (*Connection, error) {
	params := map[string]any{"sessionId": r.Session, "requestedByUserId": r.User}
	if renewEndpointID != "" {
		params["renewEndpointId"] = renewEndpointID
	}
	raw, err := rpc(ctx, "session/preview-status", params, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return statusOf(raw)
}
