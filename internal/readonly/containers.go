package readonly

// The rules for the container and cluster clients. They used to be writers by name, which
// refused `docker ps` and `kubectl get pods` as "can change the system": the common looks at
// what is running are reads. These are allowlists of the listing verbs, and anything that
// starts, stops, removes, builds, copies, execs or ships stays refused.

import (
	"fmt"
	"strings"
)

// dockerReading are the top-level docker/podman verbs that only report.
var dockerReading = map[string]bool{
	"ps": true, "images": true, "inspect": true, "logs": true, "version": true, "info": true,
	"top": true, "port": true, "diff": true, "history": true, "stats": true, "events": true,
}

// dockerGroupReading are the verbs of the management groups (`docker image ls`) that only report.
var dockerGroupReading = map[string][]string{
	"container": {"ls", "list", "ps", "inspect", "logs", "top", "port", "diff", "stats"},
	"image":     {"ls", "list", "inspect", "history"},
	"network":   {"ls", "list", "inspect"},
	"volume":    {"ls", "list", "inspect"},
	"system":    {"df", "info"},
	"compose":   {"ps", "ls", "logs", "images", "top", "port", "version", "config"},
	"context":   {"ls", "list", "inspect", "show"},
}

// containerRule allows docker and podman only in their reporting forms. Options before the
// verb (`-H host`, `--context`, `--config`) point the client at another daemon or run a
// credential helper, so they are refused; `compose config --output` writes a file.
func containerRule(args []string) (string, bool) { return containerClient("docker", args) }

// podmanRule is containerRule for podman, which takes the same verbs.
func podmanRule(args []string) (string, bool) { return containerClient("podman", args) }

func containerClient(name string, args []string) (string, bool) {
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return fmt.Sprintf("%s %s changes which daemon or configuration is used, and read-only mode cannot check it",
			name, args[0]), true
	}
	sub := firstNonFlag(args)
	if dockerReading[sub] {
		return "", false
	}
	verbs, grouped := dockerGroupReading[sub]
	if !grouped {
		return fmt.Sprintf("%s %s can change containers or images; only reporting verbs "+
			"(ps, images, inspect, logs, info, ...) are read-only", name, sub), true
	}
	rest := args[1:]
	verb := firstNonFlag(rest)
	for _, v := range verbs {
		if verb == v {
			return composeOutputRule(sub, rest)
		}
	}
	return fmt.Sprintf("%s %s %s can change containers or images; only %s is read-only",
		name, sub, verb, strings.Join(verbs, ", ")), true
}

// composeOutputRule refuses the output file option of `docker compose config`.
func composeOutputRule(group string, rest []string) (string, bool) {
	for _, a := range rest {
		name, _, _ := strings.Cut(a, "=")
		if group == "compose" && (name == "-o" || name == flagOutput) {
			return "docker compose config " + name + " writes a file", true
		}
	}
	return "", false
}

// clusterReading are the kubectl verbs that only report. `get -w` and `logs -f` stream, which
// is a long read, not a write; `-o`/`--kubeconfig`/`--server` options are limited below.
var clusterReading = map[string]bool{
	"get": true, "describe": true, "logs": true, "version": true, "explain": true,
	"api-resources": true, "api-versions": true, "cluster-info": true, "top": true, "events": true,
}

// clusterGroupReading are the verbs of the kubectl groups (`kubectl config get-contexts`) that
// only report. The others of each group select a context, edit the kubeconfig or restart things.
var clusterGroupReading = map[string][]string{
	"config":  {"get-contexts", "current-context", "get-clusters", "get-users"},
	"auth":    {"can-i", "whoami"},
	"rollout": {"status", "history"},
}

// clusterRule allows kubectl only in its reporting verbs. Options that point it at another
// cluster or run a credential plugin are refused.
func clusterRule(args []string) (string, bool) {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		switch name {
		case "--kubeconfig", "--server", "-s", "--token", "--as", "--as-group", "--context", "--cluster", "--user":
			return fmt.Sprintf("kubectl %s changes which cluster or identity is used, "+
				"and read-only mode cannot check it", name), true
		default:
			// Everything else formats or selects what is read.
		}
	}
	sub, rest := clusterVerb(args)
	if clusterReading[sub] {
		return "", false
	}
	if verbs, ok := clusterGroupReading[sub]; ok {
		verb := firstNonFlag(rest)
		for _, v := range verbs {
			if verb == v {
				return "", false
			}
		}
		return fmt.Sprintf("kubectl %s %s can change the cluster; only %s is read-only",
			sub, verb, strings.Join(verbs, ", ")), true
	}
	return fmt.Sprintf("kubectl %s can change the cluster; only get, describe, logs, top, explain, "+
		"events, version, api-resources, api-versions and cluster-info are read-only", sub), true
}

// helmReading are the helm verbs that only report on installed releases.
var helmReading = map[string]bool{
	"list": true, "ls": true, "status": true, "get": true, "history": true, "version": true,
	"show": true,
}

// helmRule allows helm only in its reporting verbs, against the configuration it already has.
func helmRule(args []string) (string, bool) {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		switch name {
		case "--kubeconfig", "--kube-context", "--kube-apiserver", "--kube-token", "--repository-config":
			return fmt.Sprintf("helm %s changes which cluster or configuration is used, "+
				"and read-only mode cannot check it", name), true
		default:
			// Everything else formats or selects what is read.
		}
	}
	sub := firstNonFlag(args)
	if helmReading[sub] {
		return "", false
	}
	return fmt.Sprintf("helm %s can change the cluster; only list, status, get, history, show and "+
		"version are read-only", sub), true
}

// clusterVerb finds the kubectl verb and what follows it, skipping a leading `-n NAMESPACE`, which
// is how kubectl is most often written (`kubectl -n kube-system get pods`).
func clusterVerb(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-n" || a == "--namespace":
			i++
		case strings.HasPrefix(a, "-"):
			// Another option: it formats or selects what is read.
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}
