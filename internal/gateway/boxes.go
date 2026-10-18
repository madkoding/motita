package gateway

import (
	"errors"
	"net/http"

	"github.com/madkoding/motita/internal/devbox"
)

// THE BOXES, as Settings → Security shows them.
//
// The agent starts a project's service in a container only Motita reaches (see devbox) and takes it
// down when its run ends. A person still wants to see what exists on their machine and to end it,
// whichever run or crashed process it belongs to: these routes list the containers carrying the
// box label and remove them, and nothing else. Ending one under a run that is using it makes the
// run's next box_get or box_shot fail with a message, which is what the person asked for.

// The container layer, as variables so the tests never touch the machine's real containers.
var (
	boxActive = devbox.Active
	boxRemove = devbox.Remove
	boxStop   = devbox.Sweep
)

// boxesView is what GET /v1/boxes answers.
type boxesView struct {
	// Runtime is the container tool found ("podman" or "docker"), "" when there is none.
	Runtime string        `json:"runtime"`
	Boxes   []devbox.Info `json:"boxes"`
}

// handleListBoxes answers which boxes exist.
func (s *Server) handleListBoxes(w http.ResponseWriter, r *http.Request) {
	rt, list, err := boxActive(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if list == nil {
		list = []devbox.Info{}
	}
	writeJSON(w, http.StatusOK, boxesView{Runtime: rt, Boxes: list})
}

// handleRemoveBox removes one box.
func (s *Server) handleRemoveBox(w http.ResponseWriter, r *http.Request) {
	switch err := boxRemove(r.Context(), r.PathValue("id")); {
	case errors.Is(err, devbox.ErrNotOurs):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleStopBoxes removes every box.
func (s *Server) handleStopBoxes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{"removed": boxStop(r.Context())})
}
