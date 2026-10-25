package readonly

import (
	"strings"
	"testing"
)

func splitLine(line string) (string, []string) {
	f := strings.Fields(line)
	return f[0], f[1:]
}

// TestContainerAndClusterClientsReadTheirReportingVerbs: `docker ps` and friends were refused
// as "can change the system" because the clients were writers by name.
func TestContainerAndClusterClientsReadTheirReportingVerbs(t *testing.T) {
	for _, line := range []string{
		"docker ps -a", "docker images", "docker logs --tail 50 web", "docker inspect web",
		"docker version", "docker info", "docker stats --no-stream", "docker top web",
		"docker port web", "docker diff web", "docker history alpine",
		"docker container ls", "docker image ls", "docker network ls", "docker volume inspect v",
		"docker system df", "docker compose ps", "docker compose logs web", "docker compose config",
		"podman ps", "podman images", "podman image ls",
		"kubectl get pods -n x", "kubectl describe pod p", "kubectl logs p", "kubectl version",
		"kubectl top nodes", "kubectl api-resources", "kubectl cluster-info",
		"helm list", "helm ls -A", "helm status r", "helm get values r", "helm history r", "helm version",
		"git version", "git symbolic-ref HEAD", "git symbolic-ref --short HEAD",
		"git notes list", "git notes show HEAD", "git sparse-checkout list", "git rerere status",
	} {
		cmd, args := splitLine(line)
		if d := Check(cmd, args); !d.Allowed {
			t.Errorf("%q should be allowed, got: %s", line, d.Reason)
		}
	}
}

// TestContainerAndClusterClientsRefuseTheirWritingVerbs holds the other side.
func TestContainerAndClusterClientsRefuseTheirWritingVerbs(t *testing.T) {
	for _, line := range []string{
		"docker run alpine", "docker rm web", "docker rmi x", "docker exec web ls", "docker stop web",
		"docker build .", "docker pull x", "docker push x", "docker cp web:/a .", "docker prune",
		"docker container rm web", "docker image rm x", "docker network create n", "docker volume rm v",
		"docker system prune", "docker compose up", "docker compose down", "docker compose exec web sh",
		"docker compose config --output out.yml", "docker compose config -o out.yml",
		"docker -H tcp://host:2375 ps", "docker --context prod ps", "docker",
		"podman run x", "podman rm x", "podman -r ps",
		"kubectl apply -f x", "kubectl delete pod p", "kubectl exec p -- ls", "kubectl edit cm c",
		"kubectl get pods --kubeconfig=/tmp/k", "kubectl get pods --server https://x", "kubectl --as admin get pods",
		"helm install r c", "helm upgrade r c", "helm uninstall r", "helm list --kubeconfig k",
		"helm search hub x",
		"git symbolic-ref HEAD refs/heads/x", "git symbolic-ref -d HEAD", "git notes add -m x",
		"git sparse-checkout set a", "git rerere forget f", "git stash",
	} {
		cmd, args := splitLine(line)
		d := Check(cmd, args)
		if d.Allowed {
			t.Errorf("%q should be refused", line)
		}
	}
}

// TestRefusedContainerCommandsSayWhy: the message names the verb, not a blanket "can change the system".
func TestRefusedContainerCommandsSayWhy(t *testing.T) {
	d := Check("docker", []string{"run", "alpine"})
	if !strings.Contains(d.Reason, "docker run") || !strings.Contains(d.Reason, "ps") {
		t.Errorf("reason should name the verb and the allowed ones: %s", d.Reason)
	}
	d = Check("podman", []string{"rm", "x"})
	if !strings.HasPrefix(d.Reason, "podman rm") {
		t.Errorf("podman should be named: %s", d.Reason)
	}
}
