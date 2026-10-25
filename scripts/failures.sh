#!/usr/bin/env bash
# Sends one failing request per common HTTP error code to the kubescope cluster,
# so you can see what each failure looks like in KubeScope.
# Pick "Failing requests" in the Steps dropdown afterwards.
set -uo pipefail

KUBESCOPE="${KUBESCOPE:-http://localhost:8090}"
CTX=kind-kubescope
PROXY=http://localhost:8011
SERVER="$(kubectl config view --context $CTX --minify -o jsonpath='{.clusters[0].cluster.server}')"

kubectl --context $CTX proxy --port=8011 >/dev/null 2>&1 &
PROXY_PID=$!
trap 'kill $PROXY_PID 2>/dev/null' EXIT
sleep 2

curl -sf -X POST "$KUBESCOPE/api/marker" -d '{"label":"Failing requests"}' >/dev/null

# try <expected code> <what> <curl args...>
try() {
  local expect=$1 what=$2
  shift 2
  local body code
  body="$(curl -sk -w '\n%{http_code}' "$@")"
  code="${body##*$'\n'}"
  body="${body%$'\n'*}"
  local reason
  reason="$(printf '%s' "$body" | python3 -c 'import json,sys
try: d=json.load(sys.stdin); print(d.get("reason",""), "-", d.get("message","")[:110])
except Exception: print("")' 2>/dev/null)"
  printf '%s %-42s %s\n' "$code" "$what" "$reason"
  [[ "$code" == "$expect" ]] || echo "    (expected $expect)"
}

JSON=(-H 'Content-Type: application/json')

try 400 "Malformed JSON" -X POST "${JSON[@]}" -d '{"kind": "Pod", oops' \
  "$PROXY/api/v1/namespaces/default/pods"

try 401 "Invalid token, straight to the API server" -H 'Authorization: Bearer not-a-real-token' \
  "$SERVER/api/v1/namespaces/default/pods"

try 403 "Anonymous user (no credentials)" \
  "$SERVER/api/v1/namespaces/default/pods"

try 404 "Pod that does not exist" \
  "$PROXY/api/v1/namespaces/default/pods/does-not-exist"

try 405 "POST to a single object (not allowed)" -X POST "${JSON[@]}" -d '{}' \
  "$PROXY/api/v1/namespaces/default/pods/some-pod"

try 409 "Create a namespace that already exists" -X POST "${JSON[@]}" \
  -d '{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"default"}}' \
  "$PROXY/api/v1/namespaces"

try 415 "PATCH with the wrong Content-Type" -X PATCH "${JSON[@]}" -d '{"spec":{"replicas":3}}' \
  "$PROXY/apis/apps/v1/namespaces/kube-system/deployments/coredns"

try 422 "Pod without containers (invalid)" -X POST "${JSON[@]}" \
  -d '{"apiVersion":"v1","kind":"Pod","metadata":{"name":"broken"},"spec":{"containers":[]}}' \
  "$PROXY/api/v1/namespaces/default/pods"
