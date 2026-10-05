# Sourced by the e2e scripts: picks the container runtime and exports it as CTR.
#
# CONTAINER_RUNTIME=podman|docker forces one. Otherwise docker is used when its daemon answers
# (what these scripts always did), then podman, which needs no daemon. When neither works CTR
# stays "docker", so the script's own error says what is missing instead of this helper's.
if [ -n "${CONTAINER_RUNTIME:-}" ]; then
  CTR="$CONTAINER_RUNTIME"
elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  CTR=docker
elif command -v podman >/dev/null 2>&1; then
  CTR=podman
else
  CTR=docker
fi
export CTR
