#!/usr/bin/env bash
# Starts KubeScope and the "kubescope" kind cluster, with the API server
# audit webhook pointing at KubeScope. ./down.sh removes both.
#
# KubeScope runs as a Docker container next to the cluster (not inside it),
# so it records the cluster being created and keeps working while you break
# things inside the cluster. History is kept in ./data and survives down/up.
#
# Settings (environment variables):
#   MODE=docker            docker (default): KubeScope in a container
#                          host: you run KubeScope yourself (make dev), for working on its code
#   AUDIT_POLICY=learning  learning (everything) or production (drops probes, leases, discovery at the source)
#   PORT=8090              where the UI is served on this machine
#   ARGS="-retention 72h"  extra KubeScope flags (see README, History)
set -euo pipefail

CLUSTER=kubescope
COLLECTOR=kubescope-collector
IMAGE=kubescope:local
MODE="${MODE:-docker}"
PORT="${PORT:-8090}"
AUDIT_POLICY="${AUDIT_POLICY:-learning}"
NODE_IMAGE="${NODE_IMAGE:-kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HERE")"
GEN="$HERE/generated"

if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "Cluster '$CLUSTER' already exists. Run make cluster-down first to recreate it."
  exit 0
fi

# kind puts its nodes on the "kind" Docker network; KubeScope joins the same network.
if ! docker network inspect kind >/dev/null 2>&1; then
  docker network create -d bridge -o com.docker.network.bridge.enable_ip_masquerade=true \
    --ipv6 --subnet fc00:f853:ccd:e793::/64 kind >/dev/null
fi

case "$MODE" in
  docker)
    echo "Building the KubeScope image..."
    docker build -q -t "$IMAGE" "$ROOT" >/dev/null
    docker rm -f "$COLLECTOR" >/dev/null 2>&1 || true
    mkdir -p "$ROOT/data"
    # Runs as you, so ./data stays yours. The UI is published on localhost only.
    docker run -d --name "$COLLECTOR" --network kind \
      --user "$(id -u):$(id -g)" \
      -v "$ROOT/data:/data" \
      -v /etc/localtime:/etc/localtime:ro \
      -p "127.0.0.1:$PORT:8090" \
      --restart unless-stopped \
      "$IMAGE" ${ARGS:-} >/dev/null
    for _ in $(seq 30); do
      curl -sf -o /dev/null "http://localhost:$PORT/api/info" && break
      sleep 1
    done
    curl -sf -o /dev/null "http://localhost:$PORT/api/info" || { docker logs "$COLLECTOR" >&2; exit 1; }
    echo "KubeScope is running: http://localhost:$PORT"
    # The API server reaches the container by name through Docker's DNS.
    COLLECTOR_ADDR="$COLLECTOR:8090"
    ;;
  host)
    # The API server reaches this machine through the gateway of the "kind" network.
    GATEWAY="$(docker network inspect kind -f '{{range .IPAM.Config}}{{if .Gateway}}{{.Gateway}} {{end}}{{end}}' | tr ' ' '\n' | grep -v ':' | head -1)"
    [[ -n "$GATEWAY" ]] || { echo "Could not find the IPv4 gateway of the 'kind' Docker network." >&2; exit 1; }
    COLLECTOR_ADDR="$GATEWAY:$PORT"
    echo "MODE=host: start KubeScope yourself (make dev), it must listen on $COLLECTOR_ADDR"
    ;;
  *) echo "MODE must be docker or host" >&2; exit 1 ;;
esac

rm -rf "$GEN" && mkdir -p "$GEN/audit"
case "$AUDIT_POLICY" in
  learning) cp "$HERE/audit-policy.yaml" "$GEN/audit/audit-policy.yaml" ;;
  production) cp "$HERE/audit-policy-production.yaml" "$GEN/audit/audit-policy.yaml" ;;
  *) echo "AUDIT_POLICY must be learning or production" >&2; exit 1 ;;
esac
echo "Audit policy: $AUDIT_POLICY"
sed "s|__COLLECTOR_ADDR__|$COLLECTOR_ADDR|" "$HERE/audit-webhook.kubeconfig.tmpl" > "$GEN/audit/audit-webhook.kubeconfig"
sed -e "s|__GENERATED_DIR__|$GEN|" -e "s|__NODE_IMAGE__|$NODE_IMAGE|" "$HERE/kind-config.yaml.tmpl" > "$GEN/kind-config.yaml"

curl -sf -m 2 -X POST "http://localhost:$PORT/api/marker" -d '{"label":"Cluster kubescope: creating"}' >/dev/null || true
kind create cluster --config "$GEN/kind-config.yaml"
kubectl config use-context "kind-$CLUSTER" >/dev/null
curl -sf -m 2 -X POST "http://localhost:$PORT/api/marker" -d '{"label":"Cluster kubescope: ready"}' >/dev/null || true
echo
echo "Ready. Open http://localhost:$PORT"
