package gateway

import (
	"context"

	"github.com/madkoding/motita/internal/config"
)

// markUnserved flags the sessions whose model is the local one while its server is down. The
// server is asked only when some session uses it, so a gateway that never ran the local model
// does not even build its manager.
func (s *Server) markUnserved(ctx context.Context, list []SessionStatus) {
	var local []int
	for i := range list {
		if list[i].Provider == config.ProviderLocal {
			local = append(local, i)
		}
	}
	if len(local) == 0 || s.localModel().Status(ctx).Server.Running {
		return
	}
	for _, i := range local {
		list[i].ModelUnserved = true
	}
}
