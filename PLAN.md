# KubeScope: plan

A learning tool that shows what happens **inside** Kubernetes when you run a command.
You type `kubectl create deployment nginx --image=nginx` and watch every component
(kubectl, API server, etcd, controller-manager, scheduler, kubelet, CoreDNS...) talk
to each other as a live sequence diagram.

Target: a **local kind cluster** that we fully control. Managed clusters (EKS, GKE, AKS)
hide the control plane, so etcd, scheduler and API server flags are not reachable there.

---

## 1. Goals

1. **See every API call**: who sent it, verb, resource, response code, latency, request and response bodies.
2. **Know who is who**: label each call with the component that made it (kubectl, scheduler, a specific controller, the kubelet on node X...).
3. **Follow the story**: group related events (Deployment → ReplicaSet → Pod → Scheduled → Running) so a single command reads as one chain.
4. **Go below the API server**: etcd writes, container runtime actions, DNS queries, Service to Pod routing.
5. **Be easy to learn from**: hide the noise by default (leases, heartbeats), explain each step in plain words, allow pause and replay.

Non goals (for now): production monitoring, multi-cluster, managed clusters, security auditing.

---

## 2. Stack

| Part | Choice | Why |
|---|---|---|
| Collector / backend | **Go** | Kubernetes is written in Go, so we get official types (`k8s.io/apiserver` audit types, `client-go`). One static binary. |
| Browser transport | **WebSocket** (`github.com/coder/websocket`) | Push events live as they happen |
| Storage | In-memory (phase 1), **SQLite** (phase 2), pluggable for bigger backends | SQLite enables recording and replaying sessions |
| Frontend | **React + TypeScript + Vite** | Common, well documented, fast dev loop |
| Visualization | Custom HTML/SVG swimlanes | No library draws a k8s sequence diagram, so we draw lanes and arrows ourselves |
| Lab cluster | **kind**: 1 control plane + 2 workers | Runs in Docker, lets us set any API server flag |
| Packaging | Frontend embedded in the Go binary (`go:embed`) | `./kubescope` serves everything |
| Desktop (later) | **Wails** | Wraps the same Go backend + React UI into a desktop app without a rewrite |

---

## 3. Architecture

```
 kind cluster "kubescope" (Docker)                 host (WSL)
┌────────────────────────────────────┐        ┌───────────────────────────────┐
│ kube-apiserver ──audit webhook─────┼──────▶ │ kubescope (Go)                 │
│ kube-apiserver ──OTel traces───────┼──────▶ │  ├ ingest/audit    (phase 1)   │
│ CoreDNS (log plugin) ──logs────────┼──────▶ │  ├ ingest/dns      (phase 3)   │
│ etcd ──watch /registry─────────────┼──────▶ │  ├ ingest/etcd     (phase 4)   │
│ containerd ──events────────────────┼──────▶ │  ├ ingest/runtime  (phase 4)   │
│ all resources ◀──client-go watch───┼─────── │  ├ classify  (who sent it)     │
└────────────────────────────────────┘        │  ├ correlate (UIDs, owners)    │
                                              │  ├ store (ring buffer/SQLite)  │
                                              │  └ http: /ws, /api, UI         │
                                              └──────────────┬────────────────┘
                                                             │ WebSocket
                                                             ▼
                                                  Browser: swimlane diagram
```

The API server reaches the collector on the host through the Docker gateway of the
`kind` network (e.g. `172.18.0.1:8090`). The setup script detects that address.

### Unified event model

Every source is converted into one shape so the UI can draw it the same way:

```
Event {
  id, time, source        // audit | dns | etcd | runtime | trace
  from, to                // lanes: "kubectl" → "apiserver", "apiserver" → "etcd" ...
  fromInstance            // node name, controller name, pod name
  verb, resource, subresource, namespace, name, uid
  code, latencyMs
  noise                   // lease | heartbeat | discovery | health | watch | ""
  summary                 // one line in plain words
  detail                  // full request/response, fetched on demand
}
```

---

## 4. Data sources per component

| Component | What we show | How we get it | Phase |
|---|---|---|---|
| kubectl / any client | Every request to the API server | API server **audit webhook** | 1 |
| API server | Response code, latency, request/response bodies | Audit webhook | 1 |
| controller-manager | Which controller created/updated what | Audit, user agent contains `system:serviceaccount:kube-system:<controller>` | 1 |
| scheduler | Binding of a pod to a node (`pods/binding`) | Audit | 1 |
| scheduler (why) | Filter and score reasons | Scheduler events + `-v=10` logs | 4 |
| kubelet | Pod status updates, node heartbeats | Audit, user `system:node:<node>` | 1 |
| kube-proxy, CoreDNS, kindnet | Their watches and reads | Audit | 1 |
| API server → etcd | Each storage call with timing | API server **OpenTelemetry tracing** (`--tracing-config-file`) | 4 |
| etcd | Keys written (`/registry/pods/default/x`) | `etcdctl watch --prefix /registry` in the control plane node | 4 |
| kubelet → containerd | Image pull, container create/start/stop | `ctr events` / CRI events on each node | 4 |
| CoreDNS | Every DNS query: client pod, name, answer, rcode | Corefile `log` plugin + pod IP → pod name map | 3 |
| Services | ClusterIP → endpoint Pod IPs | EndpointSlices via client-go watch | 3 |
| Pod network flows | Pod → Service → Pod connections | eBPF (Cilium Hubble or own probe) | 6 (optional) |

---

## 5. Phases

### Phase 1: live API call view (MVP) ✅ done (2026-10-24)
- [x] Project folder + plan
- [x] `cluster/`: kind config with audit policy + audit webhook, scripts `up.sh` / `down.sh`
- [x] Audit policy: bodies for writes, metadata only for reads (keeps volume sane), secrets never logged with bodies
- [x] Go collector: `POST /audit` receives `audit.k8s.io/v1 EventList`
- [x] Classifier: map user + user agent → lane (kubectl, scheduler, controller-manager + controller name, kubelet + node, kube-proxy, coredns, cni, storage, apiserver, other)
- [x] Noise tagging: leases, node heartbeats, discovery, health checks, watches
- [x] In-memory ring buffer (last 20k events), `GET /api/events/{id}` for bodies
- [x] WebSocket `/ws`: backlog on connect, then live stream
- [x] React UI: lanes as columns, one row per call, arrow client → API server, implied API server → etcd arrow on writes (dashed, labelled "inferred" until phase 4)
- [x] Details panel: request + response JSON, user, user agent, latency
- [x] Filters: noise categories, lanes, verbs, text search, pause, clear, marker line
- [x] Verified with `kubectl create deployment nginx --image=nginx`

### Phase 1.5: guided tour ✅ done (2026-10-24)
- [x] Server-side markers (`POST /api/marker`), Steps dropdown to focus one step
- [x] `scripts/tour.sh`: 11 steps, one scenario per component
- [x] Noise category for API server self-checks

### Phase 2: history (SQLite) ✅ done (2026-10-24)
- [x] `store` split into a live hub (WebSocket fan-out) and a persistent `Storage` interface, SQLite first
- [x] Pure Go SQLite (`modernc.org/sqlite`, no cgo), WAL mode, one file `kubescope.db`
- [x] Batched writer goroutine: inserts every 250 ms or 1000 events in one transaction, never blocks the webhook for long
- [x] Noise policy: `-store-noise=meta` (default, no bodies), `full`, `none`
- [x] Bodies gzip compressed
- [x] Retention by age (`-retention 168h`) and by size (`-max-db-size 2GB`)
- [x] `GET /api/events?from&to&hide=...` (server-side noise filtering, capped), `GET /api/histogram?from&to&buckets`
- [x] UI: Live / time range picker (presets + custom from-to), activity bar with drag-to-select
- [x] URL holds the view (`?from&to&q&step`) so a link opens the same moment
- [x] "Clear" only clears the screen; history stays in the database

### Phase 2.5: storage that lasts ✅ done (2026-10-24)
- [x] `smart` noise policy (default): pure noise never stored, ~8x less disk on an idle cluster
- [x] `audit-policy-production.yaml` drops noise at the API server; tested with the API server's policy evaluator
- [x] History indicator in the toolbar, warning when the size limit shortens history (UI + log)
- [x] Size pruning in one proportional pass; freed space returned to disk; WAL trimmed

### Phase 3: quick wins ← next
- Diff view for writes: what changed (`status.phase: Pending → Running`) instead of whole JSON
- Copy any request as curl / Postman
- Export a step as Mermaid / PlantUML sequence diagram

### Phase 4: pod startup timeline (below the API server, part 1)
- containerd events per node: sandbox (pause container), image pull, container create/start
- CNI ADD: when the pod got its IP
- Per-pod bar chart: scheduled → sandbox → IP → pulled → started → ready, with durations

### Phase 5: networking
- CoreDNS `log` plugin, tail logs, map client IP → pod; NXDOMAIN highlighted, `ndots` search path visible
- Services panel: ClusterIP → EndpointSlice → pod IPs, live
- Service traffic: which backend pod a connection went to (conntrack, later eBPF)

### Phase 6: stories (correlation)
- client-go informers keep UID → object and ownerReferences
- Object story tree: Deployment → ReplicaSets → Pods with a timeline per object
- Admission view: what kubectl sent vs what was stored (defaults and mutations)

### Phase 7: failure scenarios ("break it" mode)
- One click scenarios: ImagePullBackOff, CrashLoopBackOff, OOMKilled, Pending (resources, taints),
  wrong Service selector, failing readiness probe, RBAC 403
- Each explains the pattern to look for in the diagram

### Phase 8: deep internals
- API server tracing (OTLP receiver) → real apiserver → etcd timing, admission webhook latency
- etcd key watch `/registry`, revisions
- Scheduler reasoning: per node filter result and score

### Phase 9: real clusters (on-prem) — see section 8
- In-cluster install (Helm chart), node agent DaemonSet, TLS + auth, HA API servers, file-based audit ingestion

### Phase 10: extras
- Animated slow-motion replay, built-in terminal, Wails desktop app, eBPF flows, guided lessons

---

## 6. Repository layout

```
kubescope/
├── PLAN.md
├── README.md
├── Makefile
├── cluster/
│   ├── kind-config.yaml.tmpl
│   ├── audit-policy.yaml
│   ├── up.sh  down.sh
│   └── generated/            (git ignored, rendered config)
├── collector/                (Go)
│   ├── main.go
│   ├── internal/audit/       ingest + classify
│   ├── internal/store/       ring buffer
│   ├── internal/server/      http + websocket + embedded UI
│   └── internal/model/       unified event
└── web/                      (React + TS + Vite)
    └── src/
```

---

## 7. Risks and how we handle them

| Risk | Mitigation |
|---|---|
| Event volume (watches, heartbeats every few seconds) | Noise tags on the server, hidden by default in the UI, ring buffer cap |
| Large list responses | Policy logs reads at Metadata level only |
| Secrets in logs | Policy logs secrets, configmaps and tokens at Metadata level only |
| Collector down blocks the API server | Webhook in `batch` mode drops events instead of blocking |
| Audit only shows the API layer | Phases 3 and 4 add DNS, etcd, runtime sources |
| kubeadm config format changes between k8s versions | Pin the kind node image in the config |

---

## 8. Running on a real cluster (e.g. 10 node on-prem)

The kind setup is for learning. On a real self-managed cluster the same design works with these changes.

### Volume
A 10 node cluster with real workloads sends roughly 50 to 500 audit events per second
(each kubelet renews a lease every 10 s, controllers react to every change).
- Batched SQLite writes handle a few thousand inserts per second on one disk.
- Noise is stored without bodies, or not at all (`-store-noise`).
- A lighter audit policy for production: `None` for leases, health checks and events reads,
  `Metadata` for everything except writes to workloads.
- Retention by age and by size, so the disk never fills.
- `Storage` is an interface, so a bigger backend (PostgreSQL, ClickHouse) can be added later without touching the UI.

### Where the collector runs
In the cluster, not on a laptop:
- Deployment (1 replica) + PersistentVolumeClaim for the database, installed with a Helm chart.
- The API server runs with host networking and cannot resolve cluster DNS, so the webhook points to
  a fixed address: a NodePort on the control plane nodes or a ClusterIP (reachable when kube-proxy runs there).
- UI exposed through an Ingress or `kubectl port-forward`.

### Getting audit events
Two options, documented side by side:
1. **Webhook** (live, what we use now): add `--audit-policy-file` and `--audit-webhook-config-file` to
   `/etc/kubernetes/manifests/kube-apiserver.yaml` on **every** control plane node. The kubelet restarts
   the API server; do one node at a time.
2. **Audit log files** (no API server change if audit logging is already on): a DaemonSet on control plane
   nodes tails `/var/log/kubernetes/audit/audit.log` and forwards to the collector.

### High availability control plane
Usually 3 API servers behind a load balancer. Each request is handled by one of them, so there are no
duplicates, but each stream must be configured. Events are tagged with the API server that handled them
(an API server instance per lane).

### Node level data
DNS, containerd and CNI data live on each node. A small `kubescope-agent` DaemonSet collects them and sends
them to the collector (replaces `docker exec` used with kind).

### Security
- Audit events can contain sensitive data. Secrets, ConfigMaps and tokens are never stored with bodies (already the case).
- Webhook over TLS with a bearer token; UI behind login (OIDC or a static token to start).
- Collector service account is read-only (list/watch for correlation).
- Batch webhook mode: if the collector is down, the API server drops events; it never blocks the cluster.
- API server cost: `RequestResponse` level uses CPU and memory; production policy keeps it to workload writes.

### UI at scale
- Filters by node, namespace and component instance.
- Server-side filtering and the activity bar to find busy moments without loading everything.
