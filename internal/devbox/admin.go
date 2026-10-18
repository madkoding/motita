package devbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// WHAT EXISTS, FOR A PERSON TO SEE AND STOP.
//
// The runs keep their own boxes, but a person looking at Settings wants to know what is running on
// their machine and to be able to end it, whichever run (or crashed process) it belongs to. Every
// container this package starts carries Label, which is all these functions look at.

// ErrNotOurs is Remove's answer for a container that is not one of ours.
var ErrNotOurs = errors.New("not one of Motita's boxes")

// Info is one container of ours.
type Info struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	Status  string `json:"status"`
	Created string `json:"created"`
}

// filterFlag is the runtime's filter option, whose value is always a label test.
const (
	filterFlag = "--filter"
	labelTest  = "label=" + Label
)

const infoFormat = "{{.ID}}|{{.Names}}|{{.Image}}|{{.Status}}|{{.CreatedAt}}"

// Active lists the containers of ours that exist, running or not, and the runtime that holds them.
// With no runtime it answers an empty list and no error: there is nothing to show.
func Active(ctx context.Context) (string, []Info, error) {
	rt, err := Detect(ctx)
	if err != nil {
		return "", nil, nil
	}
	out, err := runCmd(ctx, rt.Bin, "ps", "-a", filterFlag, labelTest, "--format", infoFormat)
	if err != nil {
		return rt.Bin, nil, fmt.Errorf("%s could not list the boxes: %v", rt.Bin, err)
	}
	var list []Info
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "|", 5)
		if len(f) == 5 {
			list = append(list, Info{ID: f[0], Name: f[1], Image: f[2], Status: f[3], Created: f[4]})
		}
	}
	return rt.Bin, list, nil
}

// Remove stops and removes one of our containers, named by its id or its name. Anything that is not
// in Active is refused: this is not a way to remove other containers.
func Remove(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("no box named")
	}
	bin, list, err := Active(ctx)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Name == ref || (len(ref) >= 6 && strings.HasPrefix(c.ID, ref)) {
			if out, err := runCmd(ctx, bin, "rm", "-f", c.ID); err != nil {
				return fmt.Errorf("%s could not remove it: %v\n%s", bin, err, tail(string(out), 500))
			}
			tidy(ctx, bin)
			return nil
		}
	}
	return fmt.Errorf("%q: %w", ref, ErrNotOurs)
}

// tidy removes the networks and images of ours that no container uses any more.
func tidy(ctx context.Context, bin string) {
	_, _ = runCmd(ctx, bin, "network", "prune", "-f", filterFlag, labelTest)
	_, _ = runCmd(ctx, bin, "image", "prune", "-a", "-f", filterFlag, labelTest)
}

// Sweep removes every container of ours that exists, with its networks and images: what earlier
// processes left behind (a crash, a kill) when the gateway starts, and "stop all" in Settings.
// It returns how many containers it removed.
func Sweep(ctx context.Context) int {
	bin, list, err := Active(ctx)
	if err != nil || len(list) == 0 {
		return 0
	}
	n := 0
	for _, c := range list {
		if _, err := runCmd(ctx, bin, "rm", "-f", c.ID); err == nil {
			n++
		}
	}
	tidy(ctx, bin)
	return n
}
