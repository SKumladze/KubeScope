#!/usr/bin/env bash
# Removes everything KubeScope created on this machine: the cluster, the KubeScope
# container, the recorded history, the KubeScope image and the base images it
# downloaded. Asks before deleting.
#
#   make cluster-down-all            show what will be deleted, ask, delete
#   DRY_RUN=1 make cluster-down-all  only show what would be deleted
#   YES=1 make cluster-down-all      no question (for scripts)
#
# The "kind" Docker network is removed only when no other container (running or
# stopped) uses it. Docker's build cache is never touched: other projects share it.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HERE")"
# Same node image as up.sh. It is pulled by digest, so it has no tag to look it up by.
NODE_IMAGE="$(sed -n 's/^NODE_IMAGE="\${NODE_IMAGE:-\(.*\)}"$/\1/p' "$HERE/up.sh")"
NODE_IMAGE="kindest/node@${NODE_IMAGE#*@}"
# Images this project pulls or builds (see Dockerfile and up.sh).
IMAGES=(kubescope:local "$NODE_IMAGE" node:24-alpine golang:1.27-alpine gcr.io/distroless/static-debian12:nonroot)

image_size() { docker image inspect "$1" --format '{{.Size}}' 2>/dev/null | awk '{printf "%.0f MB", $1/1048576}'; }
ours='^(kubescope-control-plane|kubescope-worker2?|kubescope-collector)$'
used_by_other_containers() {
  # A base image still used by a container we don't own (e.g. another kind cluster) is kept.
  local id
  id="$(docker image inspect "$1" --format '{{.Id}}')"
  docker ps -a --filter "ancestor=$id" --format '{{.Names}}' | grep -vE "$ours" || true
}
# Every KubeScope build: labelled ones, plus older unnamed builds from before the
# label existed (recognised by their entrypoint). Rebuilds leave the previous build unnamed.
old_kubescope_builds() {
  docker images -q --filter label=io.kubescope.image=collector --filter dangling=true
  for id in $(docker images -q --filter dangling=true); do
    if [[ "$(docker image inspect "$id" --format '{{index .Config.Entrypoint 0}}' 2>/dev/null)" == /kubescope ]]; then
      echo "$id"
    fi
  done
}
other_kind_network_users() {
  docker ps -a --filter network=kind --format '{{.Names}}' | grep -vE "$ours" || true
}

echo "This removes everything KubeScope created on this machine:"
echo
kind get clusters 2>/dev/null | grep -qx kubescope && echo "  • kind cluster \"kubescope\" (3 node containers)"
docker inspect kubescope-collector >/dev/null 2>&1 && echo "  • KubeScope container"
if ls "$ROOT"/data/kubescope.db* >/dev/null 2>&1; then
  echo "  • HISTORY: data/kubescope.db ($(du -ch "$ROOT"/data/kubescope.db* | tail -1 | cut -f1)), cannot be undone"
fi
remove_images=()
for img in "${IMAGES[@]}"; do
  docker image inspect "$img" >/dev/null 2>&1 || continue
  others="$(used_by_other_containers "$img")"
  if [[ -n "$others" ]]; then
    echo "  – keeping image $img: used by $(echo $others | tr ' ' ',')"
  else
    echo "  • image $img ($(image_size "$img"))"
    remove_images+=("$img")
  fi
done
old_builds=($(old_kubescope_builds | sort -u))
if (( ${#old_builds[@]} )); then
  echo "  • ${#old_builds[@]} old unnamed KubeScope build(s) ($(for id in "${old_builds[@]}"; do docker image inspect "$id" --format '{{.Size}}'; done | awk '{s+=$1} END {printf "%.0f MB", s/1048576}'))"
fi
[[ -d "$HERE/generated" ]] && echo "  • generated cluster config (cluster/generated)"
remove_network=false
if docker network inspect kind >/dev/null 2>&1; then
  others="$(other_kind_network_users)"
  if [[ -n "$others" ]]; then
    echo "  – keeping the \"kind\" Docker network: used by $(echo $others | tr ' ' ',')"
  else
    echo "  • the \"kind\" Docker network (nothing else uses it)"
    remove_network=true
  fi
fi
echo
echo "Kept: Docker's build cache (shared with other projects)."
echo

if [[ "${DRY_RUN:-}" == 1 ]]; then
  echo "DRY_RUN=1: nothing was deleted."
  exit 0
fi
if [[ "${YES:-}" != 1 ]]; then
  if [[ ! -t 0 ]]; then
    echo "Not running in a terminal: set YES=1 to confirm." >&2
    exit 1
  fi
  read -rp "Delete all of this, including the history? [y/N] " answer
  [[ "$answer" == y || "$answer" == Y ]] || { echo "Nothing deleted."; exit 0; }
fi

"$HERE/down.sh" 2>/dev/null || true
rm -f "$ROOT"/data/kubescope.db "$ROOT"/data/kubescope.db-wal "$ROOT"/data/kubescope.db-shm
rmdir "$ROOT/data" 2>/dev/null || true
rm -rf "$HERE/generated"
for img in "${remove_images[@]}"; do
  docker rmi "$img" >/dev/null && echo "Removed image $img"
done
if (( ${#old_builds[@]} )); then
  docker rmi "${old_builds[@]}" >/dev/null && echo "Removed ${#old_builds[@]} old KubeScope build(s)"
fi
if $remove_network; then
  docker network rm kind >/dev/null && echo "Removed the \"kind\" Docker network"
fi
echo "Done. make cluster-up starts again from scratch (it downloads the images again, about 1.5 GB)."
