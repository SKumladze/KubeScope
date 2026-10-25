#!/usr/bin/env bash
# Guided tour: runs one small scenario per Kubernetes component and drops a
# labelled marker in KubeScope before each step. In the UI, click a marker to
# see only the requests that step caused.
#
#   ./scripts/tour.sh          wait for Enter between steps
#   ./scripts/tour.sh --auto   run everything with a pause between steps
set -euo pipefail

KUBESCOPE="${KUBESCOPE:-http://localhost:8090}"
NS=tour
AUTO=false
DELAY="${DELAY:-12}"
[[ "${1:-}" == "--auto" || ! -t 0 ]] && AUTO=true

if ! curl -sf -o /dev/null "$KUBESCOPE/"; then
  echo "KubeScope is not running at $KUBESCOPE. Start it with ./bin/kubescope" >&2
  exit 1
fi
kubectl config use-context kind-kubescope >/dev/null

step=0
step() {
  step=$((step + 1))
  local title="Step $step: $1"
  echo
  echo "━━━ $title"
  echo "    Look for: $2"
  curl -sf -X POST "$KUBESCOPE/api/marker" -d "$(printf '{"label":"%s"}' "$title")" >/dev/null
}

run() {
  echo "    \$ $*"
  "$@" 2>&1 | sed 's/^/      /'
}

next() {
  if $AUTO; then sleep "$DELAY"; else read -rp "    Press Enter for the next step… " _; fi
}

kubectl delete namespace "$NS" --ignore-not-found --wait >/dev/null

step "kubectl reads (kubectl → API server)" \
  "one GET from the kubectl lane. Reads carry no body, only metadata."
run kubectl get nodes
next

step "Create a namespace (kubectl write → etcd)" \
  "kubectl POST namespaces, the orange etcd arrow (stored), then controllers create the default ServiceAccount and the kube-root-ca.crt ConfigMap in it."
run kubectl create namespace "$NS"
next

step "Create a deployment (controllers → scheduler → kubelet)" \
  "deployment-controller creates a ReplicaSet, replicaset-controller creates a Pod, scheduler POSTs pods/binding, the kubelet on that node reports Pulling, Created, Started."
run kubectl -n "$NS" create deployment web --image=nginx
run kubectl -n "$NS" rollout status deployment web --timeout=120s
next

step "Scale to 3 (scheduler spreads pods across nodes)" \
  "replicaset-controller creates 2 pods, scheduler binds them (check the target node in each binding body), two different kubelets answer."
run kubectl -n "$NS" scale deployment web --replicas=3
run kubectl -n "$NS" rollout status deployment web --timeout=120s
next

step "Expose a Service (endpointslice controller)" \
  "kubectl POST services, the endpointslice-controller creates an EndpointSlice with the 3 pod IPs. kube-proxy and CoreDNS learn about it through watches they already have open (turn on 'watches' to see them)."
run kubectl -n "$NS" expose deployment web --port=80
next

step "Pod uses DNS + Service (kubelet, but DNS itself is invisible here)" \
  "a new pod scheduled and started. Its DNS query to CoreDNS and the HTTP call through kube-proxy rules never reach the API server, so they don't appear yet (phase 3 adds CoreDNS logs)."
run kubectl -n "$NS" run client --image=busybox:1.36 --restart=Never --command -- sh -c "nslookup web.$NS.svc.cluster.local; wget -qO- http://web | head -4"
run kubectl -n "$NS" wait pod/client --for=jsonpath='{.status.phase}'=Succeeded --timeout=120s
run kubectl -n "$NS" logs client
next

step "Persistent volume (storage provisioner)" \
  "the PVC waits until a pod uses it. Then the scheduler picks a node, local-path-provisioner (storage lane) creates a helper pod and a PersistentVolume, and the pv-controller binds them."
kubectl -n "$NS" apply -f - <<'EOF' | sed 's/^/      /'
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: data
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 100Mi
---
apiVersion: v1
kind: Pod
metadata:
  name: writer
spec:
  containers:
    - name: writer
      image: busybox:1.36
      command: ["sh", "-c", "echo hello > /data/hello && sleep 3600"]
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: data
EOF
run kubectl -n "$NS" wait pod/writer --for=condition=Ready --timeout=120s
next

step "Kill a pod (self-healing)" \
  "kubectl DELETE pod, the kubelet stops it and confirms the delete, replicaset-controller notices 2 of 3 and creates a replacement, scheduler binds it."
POD="$(kubectl -n "$NS" get pods -l app=web -o jsonpath='{.items[0].metadata.name}')"
run kubectl -n "$NS" delete pod "$POD"
run kubectl -n "$NS" rollout status deployment web --timeout=120s
next

step "Rolling update (new ReplicaSet, old one scaled down)" \
  "kubectl PATCH deployment, deployment-controller creates a second ReplicaSet and moves replicas over one by one, pods are created and deleted in turns."
run kubectl -n "$NS" set image deployment/web nginx=nginx:alpine
run kubectl -n "$NS" rollout status deployment web --timeout=180s
next

step "Cordon a node (node object, scheduler respects it)" \
  "kubectl PATCH nodes setting spec.unschedulable. Then uncordon."
run kubectl cordon kubescope-worker2
run kubectl uncordon kubescope-worker2
next

step "Delete the namespace (namespace controller + garbage collector)" \
  "kubectl DELETE namespace, then namespace-controller and the kubelets delete everything inside: pods, ReplicaSets, Services, PVC, PV."
run kubectl delete namespace "$NS" --wait=false
sleep "$DELAY"

echo
echo "Tour finished. In KubeScope, click any step marker to see only what it caused."
