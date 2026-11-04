package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/localmodel"
)

// MOTITA'S LOCAL MODEL FROM THE TERMINAL: /local, the same steps as Settings → Providers in the
// browser. The gateway owns the download and the server, so the interface only asks it.

// localModelRunner is a runner that reaches the gateway's local model: the gateway client.
type localModelRunner interface {
	LocalModel(ctx context.Context, action string) (localmodel.Status, error)
}

// localActions are the steps /local takes; "" shows the status.
var localActions = map[string]bool{"": true, "install": true, "cancel": true, "start": true, "stop": true,
	"delete": true}

// runLocal is /local [install|cancel|start|stop|delete [confirm]].
func (t *TUI) runLocal(ctx context.Context, arg string) {
	defer t.drawFrame()
	lm, ok := t.Runner.(localModelRunner)
	if !ok {
		t.addMessage(AuthorSystem, t.tr(
			"local: this interface is not attached to a gateway, and the gateway is what runs the local model."))
		return
	}
	fields := strings.Fields(strings.ToLower(arg))
	action := ""
	if len(fields) > 0 {
		action = fields[0]
	}
	if !localActions[action] {
		t.addMessage(AuthorSystem, t.tr("usage: /local [install | cancel | start | stop | delete]"))
		return
	}
	if action == "delete" && (len(fields) < 2 || fields[1] != "confirm") {
		t.addMessage(AuthorSystem, t.trf(
			"local: this deletes the model and its runtime (%s). Type /local delete confirm to go ahead.",
			gigabytes(localmodel.ModelSize)))
		return
	}
	st, err := lm.LocalModel(ctx, action)
	if err != nil {
		t.addMessage(AuthorSystem, t.trf("local: %s", err.Error()))
		return
	}
	t.addPreformatted(AuthorSystem, t.localReport(st, action))
}

func gigabytes(bytes int64) string { return fmt.Sprintf("%.1f GB", float64(bytes)/1e9) }

func vramText(mb int) string { return fmt.Sprintf("%.1f GB", float64(mb)/1024) }

// localReport is the status as a few lines, ending with the next step to type.
func (t *TUI) localReport(st localmodel.Status, action string) string {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }
	line(t.trf("Local model: %s", st.ModelName))
	if st.GPU == nil {
		line(t.tr("GPU: none found"))
	} else {
		line(t.trf("GPU: %s · %s", st.GPU.Name, vramText(st.GPU.TotalMB)))
	}
	if !st.Eligible {
		line(t.tr("Not available here: the local model needs a GPU with at least 4 GB of memory."))
		return strings.TrimRight(b.String(), "\n")
	}
	switch {
	case st.Task.Running:
		line(t.trf("Downloading %s: %d%% (/local again shows the progress, /local cancel stops it)",
			t.tr(phaseName(st.Task.Phase)), percent(st.Task)))
	case !st.ModelPresent || !st.RuntimePresent:
		line(t.trf("Not installed. /local install downloads it (%s).", gigabytes(st.Size)))
	case st.Server.Running:
		line(t.trf("Running · %d tokens of context · port %d. Pick it with /models %s; /local stop stops it.",
			st.Server.Context, st.Server.Port, st.ModelID))
	case st.Server.Starting:
		line(t.tr("Loading the model into the GPU… /local again shows when it is ready."))
	default:
		line(t.tr("Installed · stopped. /local start starts it."))
	}
	if st.Task.Error != "" {
		line(t.trf("Last download: %s", st.Task.Error))
	}
	if e := st.Server.Error; e != "" && e != "stopped" {
		line(t.trf("Server: %s", e))
	}
	if action == "delete" {
		line(t.tr("Deleted."))
	}
	return strings.TrimRight(b.String(), "\n")
}

func phaseName(phase string) string {
	if phase == "model" {
		return "the model"
	}
	return "the runtime (llama.cpp)"
}

// percent is how far a download is, 0 while its size is unknown.
func percent(task localmodel.Task) int64 {
	if task.Total <= 0 {
		return 0
	}
	return min(100, task.Done*100/task.Total)
}
