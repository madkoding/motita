package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/madkoding/motita/internal/peers"
)

// PeerRequests is what `motita gateway computers` shows: this computer, whether other computers
// can reach it, and the ones asking to be let in.
type PeerRequests struct {
	Name      string          `json:"name"`
	Reachable bool            `json:"reachable"`
	Requests  []peers.Request `json:"requests"`
	Grants    []peers.Grant   `json:"grants"`
}

// ReadPeerRequests reads the computers asking to be let into the gateway at baseURL, with its
// bearer token: a computer without a screen answers them from its terminal.
func ReadPeerRequests(ctx context.Context, baseURL, token string) (PeerRequests, error) {
	var view peersView
	if err := peerCall(ctx, http.MethodGet, baseURL, "/v1/peers", token, nil, &view); err != nil {
		return PeerRequests{}, err
	}
	return PeerRequests{Name: view.Self.Name, Reachable: view.Self.Reachable, Requests: view.Requests,
		Grants: view.Grants}, nil
}

// DecidePeerRequest lets the computer that asked with request id in, or not.
func DecidePeerRequest(ctx context.Context, baseURL, token, id string, allow bool) error {
	body, _ := json.Marshal(map[string]bool{"allow": allow})
	return peerCall(ctx, http.MethodPost, baseURL, "/v1/peer-requests/"+url.PathEscape(id), token, body, nil)
}

// peerCall is one request to this computer's own gateway, decoding the answer into out when given.
func peerCall(ctx context.Context, method, baseURL, path, token string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", bearerPrefix+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return refusalError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
