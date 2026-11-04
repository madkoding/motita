package gateway

import (
	"context"
	"net/http"

	"github.com/madkoding/motita/internal/localmodel"
)

// LocalModel asks the gateway for Motita's local model: its status with action "", or one of
// "install", "cancel", "start", "stop" and "delete", answered with the status after it. The text
// interface's /local is built on it.
func (c *Client) LocalModel(ctx context.Context, action string) (localmodel.Status, error) {
	method, path := http.MethodPost, "/v1/local-model/"+action
	switch action {
	case "":
		method, path = http.MethodGet, "/v1/local-model"
	case "delete":
		method, path = http.MethodDelete, "/v1/local-model"
	}
	var st localmodel.Status
	err := c.do(ctx, method, path, nil, &st)
	return st, err
}
