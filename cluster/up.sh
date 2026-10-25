#!/usr/bin/env bash
# Creates the "kubescope" kind cluster with the API server audit webhook
# pointing at the collector running on this machine.
set -euo pipefail

CLUSTER=kubescope
NODE_IMAGE="${NODE_IMAGE:-kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f}"
COLLECTOR_PORT="${COLLECTOR_PORT:-8090}"
# learning (default): everything, noise included. production: drops probes, leases, discovery at the source.
AUDIT_POLICY="${AUDIT_POLICY:-learning}"
HERE="$(cd "$(dirname "$0")" && pwd)"
GEN="$HERE/generated"

if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "Cluster '$CLUSTER' already exists. Run ./down.sh first to recreate it."
  exit 0
fi

# kind puts its nodes on the "kind" Docker network. The API server reaches
# the host through that network's gateway, so we need it before the cluster exists.
if ! docker network inspect kind >/dev/null 2>&1; then
  docker network create -d bridge -o com.docker.network.bridge.enable_ip_masquerade=true \
    --ipv6 --subnet fc00:f853:ccd:e793::/64 kind >/dev/null
fi
GATEWAY="$(docker network inspect kind -f '{{range .IPAM.Config}}{{if .Gateway}}{{.Gateway}} {{end}}{{end}}' | tr ' ' '\n' | grep -v ':' | head -1)"
if [[ -z "$GATEWAY" ]]; then
  echo "Could not find the IPv4 gateway of the 'kind' Docker network." >&2
  exit 1
fi
echo "Collector address seen from the cluster: $GATEWAY:$COLLECTOR_PORT"

rm -rf "$GEN" && mkdir -p "$GEN/audit"
case "$AUDIT_POLICY" in
  learning) cp "$HERE/audit-policy.yaml" "$GEN/audit/audit-policy.yaml" ;;
  production) cp "$HERE/audit-policy-production.yaml" "$GEN/audit/audit-policy.yaml" ;;
  *) echo "AUDIT_POLICY must be learning or production" >&2; exit 1 ;;
esac
echo "Audit policy: $AUDIT_POLICY"
sed "s|__COLLECTOR_ADDR__|$GATEWAY:$COLLECTOR_PORT|" "$HERE/audit-webhook.kubeconfig.tmpl" > "$GEN/audit/audit-webhook.kubeconfig"
sed -e "s|__GENERATED_DIR__|$GEN|" -e "s|__NODE_IMAGE__|$NODE_IMAGE|" "$HERE/kind-config.yaml.tmpl" > "$GEN/kind-config.yaml"

kind create cluster --config "$GEN/kind-config.yaml"
kubectl config use-context "kind-$CLUSTER" >/dev/null
echo
echo "Cluster ready. Start the collector (make run) and open http://localhost:$COLLECTOR_PORT"
