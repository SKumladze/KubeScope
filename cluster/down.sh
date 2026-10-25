#!/usr/bin/env bash
# Removes the "kubescope" kind cluster and the KubeScope container.
# History in ./data is kept (make clean-history deletes it).
set -euo pipefail
kind delete cluster --name kubescope
if docker inspect kubescope-collector >/dev/null 2>&1; then
  # stop sends SIGTERM first, so KubeScope saves queued events before it exits
  docker stop -t 10 kubescope-collector >/dev/null
  docker rm kubescope-collector >/dev/null
  echo "Removed the KubeScope container (history kept in ./data)"
fi
