package gateway

import (
	"net/http"

	"github.com/madkoding/motita/internal/tui"
)

// handleListCommands returns the slash command catalogue so a front end can
// build an autocomplete popup with the same commands the TUI offers.
func (s *Server) handleListCommands(w http.ResponseWriter, _ *http.Request) {
	cmds := tui.Commands()
	type cmdView struct {
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
		Help    string   `json:"help"`
		Arg     string   `json:"arg,omitempty"`
		Group   string   `json:"group"`
	}
	views := make([]cmdView, 0, len(cmds))
	for _, c := range cmds {
		aliases := c.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		views = append(views, cmdView{
			Name:    c.Name,
			Aliases: aliases,
			Help:    c.Help,
			Arg:     c.Arg,
			Group:   c.Group,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": views})
}
