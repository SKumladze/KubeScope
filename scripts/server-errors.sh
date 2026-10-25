#!/usr/bin/env bash
# Causes 5xx errors on purpose by breaking things the API server depends on.
# Everything is scoped to the "errors-lab" namespace or a fake API group and
# is removed at the end, so the rest of the cluster keeps working.
# Pick "Server errors (5xx)" in the KubeScope Steps dropdown afterwards.
set -uo pipefail

KUBESCOPE="${KUBESCOPE:-http://localhost:8090}"
CTX=kind-kubescope
NS=errors-lab
K="kubectl --context $CTX"

cleanup() {
  echo
  echo "Cleaning up..."
  $K delete validatingwebhookconfiguration kubescope-broken-webhook --ignore-not-found >/dev/null
  $K delete apiservice v1.broken.kubescope.dev --ignore-not-found >/dev/null
  $K delete namespace $NS --ignore-not-found --wait=false >/dev/null
}
trap cleanup EXIT

echo "Setting up broken dependencies in namespace $NS..."
# A namespace left over from a previous run must be fully gone first:
# nothing can be created in a namespace that is still terminating.
$K delete namespace $NS --ignore-not-found --wait=true --timeout=120s >/dev/null
$K create namespace $NS >/dev/null
$K label namespace $NS kubescope-lab=broken-webhook >/dev/null

# 1. Admission webhook pointing to a Service that does not exist.
#    Only ConfigMaps in namespaces labelled kubescope-lab=broken-webhook are affected.
$K apply -f - >/dev/null <<EOF
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: kubescope-broken-webhook
webhooks:
  - name: broken.kubescope.dev
    failurePolicy: Fail
    sideEffects: None
    admissionReviewVersions: ["v1"]
    timeoutSeconds: 3
    namespaceSelector:
      matchLabels:
        kubescope-lab: broken-webhook
    rules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE"]
        resources: ["configmaps"]
    clientConfig:
      service:
        name: does-not-exist
        namespace: $NS
        path: /validate
EOF

# 2. An aggregated API whose backend Service does not exist.
$K apply -f - >/dev/null <<EOF
apiVersion: apiregistration.k8s.io/v1
kind: APIService
metadata:
  name: v1.broken.kubescope.dev
spec:
  group: broken.kubescope.dev
  version: v1
  groupPriorityMinimum: 10
  versionPriority: 10
  insecureSkipTLSVerify: true
  service:
    name: does-not-exist
    namespace: $NS
EOF

# 3. A pod with nothing listening on port 8080, and a Service with no pods behind it.
$K -n $NS run silent --image=busybox:1.36 --restart=Never --command -- sleep 3600 >/dev/null
$K -n $NS create service clusterip empty --tcp=80:80 >/dev/null
$K -n $NS wait pod/silent --for=condition=Ready --timeout=90s >/dev/null
sleep 2

curl -sf -X POST "$KUBESCOPE/api/marker" -d '{"label":"Server errors (5xx)"}' >/dev/null
echo

# try <what> <kubectl args...>: prints the HTTP code the API server answered with.
try() {
  local what=$1
  shift
  local out code
  out="$($K "$@" -v=6 2>&1)"
  # kubectl -v=6 logs: "Response" verb="GET" url="..." status="503 Service Unavailable"
  code="$(grep -oE 'status="[0-9]{3}' <<<"$out" | tail -1 | grep -oE '[0-9]{3}')"
  local msg
  msg="$(grep -E '^(Error|error)' <<<"$out" | head -1 | cut -c1-120)"
  printf '%s %-44s %s\n' "${code:-???}" "$what" "$msg"
}

try "Create ConfigMap, webhook unreachable" -n $NS create configmap test --from-literal=a=b
try "Read from an aggregated API that is down" get --raw /apis/broken.kubescope.dev/v1
try "Proxy to a pod port with nothing listening" get --raw "/api/v1/namespaces/$NS/pods/silent:8080/proxy/"
try "Proxy to a Service with no pods" get --raw "/api/v1/namespaces/$NS/services/empty:80/proxy/"

sleep 2
