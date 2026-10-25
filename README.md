# KubeScope

See what Kubernetes does behind the scenes. Run a `kubectl` command and watch every
component (kubectl, API server, etcd, controllers, scheduler, kubelet...) talk to each
other as a live sequence diagram.

Works with a local **kind** cluster that KubeScope creates and configures. See [PLAN.md](PLAN.md)
for the roadmap.

## Requirements

Docker, kind, kubectl, Go 1.24+, Node 20+.

## Quick start

```bash
make build          # builds the UI and the Go binary into bin/kubescope
make cluster-up     # creates the "kubescope" kind cluster (3 nodes) with the audit webhook
./bin/kubescope     # start the collector, then open http://localhost:8090
make demo           # in another terminal: create, scale and delete a deployment
```

The collector must be running for events to show up. If it is down, the API server
drops audit events after a few retries; the cluster keeps working.

## History

Everything is saved to `kubescope.db` (SQLite, one file) and survives restarts.

| Flag | Default | Meaning |
|---|---|---|
| `-db` | `kubescope.db` | database file |
| `-retention` | `168h` (7 days) | delete events older than this, `0` keeps forever |
| `-max-db-mb` | `2048` | when the file grows past this, the oldest events are deleted, even if younger than the retention |
| `-store-noise` | `smart` | what noise goes into history (it is always shown live): `smart` drops leases, health checks, discovery and API server self-checks, keeps watches, heartbeats and auth without bodies; `meta` keeps all noise without bodies; `full`; `none` |

Both limits apply, whichever is reached first. The toolbar shows how far back history goes
and how full the database is (**History since ... · 17 MB of 2.0 GB**). It turns orange with ⚠ when
the size limit is deleting history before the retention, and the collector logs a warning with how much is kept.

Measured on the idle kind cluster: 94% of audit events are noise. With `smart`, storage is about
**30 MB per day** (about 2 months in 2 GB) instead of about 255 MB per day with everything stored.

### Real clusters: drop noise at the source

`cluster/audit-policy-production.yaml` tells the API server not to generate health check, discovery,
lease, heartbeat and self-check events at all. That saves API server CPU and memory too, not just disk.
Use it for kind with `AUDIT_POLICY=production ./cluster/up.sh`, or as the `--audit-policy-file` on a real
cluster. Both policies are checked by tests using the API server's own policy evaluator.

In the UI:
- **Live / 15m / 1h / 6h / 24h / 7d** switch between the live stream and stored history.
- The **activity bar** shows calls per time slice (dark = writes). Drag across it to load that range,
  click a blue tick to open that tour step. **Zoom in** makes the bar show only the selected range.
- The two date fields set an exact range, to the second.
- **Copy link** copies a URL that opens exactly this view (range, search, status, verbs, namespace and step), handy for bug reports.

## Using it

| Control | What it does |
|---|---|
| **Add marker** | Draws a line, then run a command: everything below it happened after. |
| **Clear screen** | Clears the live view only. Nothing is deleted from history. |
| **Steps** dropdown | Show only the events between one marker and the next. |
| **Search** | Words separated by spaces, all must match: `nginx pods`, `scheduler`, a UID. Fields: `code:409` `code:4xx` `verb:create` `ns:default` `res:pods` `name:web` `from:scheduler` `node:kubescope-worker` `user:admin`, latency `ms>100` |
| **Status** chips | Show only 1xx / 2xx / 3xx / 4xx / 5xx (1xx = exec, attach, port-forward switching protocols). Click several to combine (e.g. 4xx + 5xx = all errors). None selected = everything |
| **Verbs** chips | Show only get / list / watch / create / update / patch / delete, combinable |
| **Namespace** | Show one namespace |
| **Reset filters** | Clears status, verbs, namespace, writes only and search in one click |
| **Writes only** | Hide reads. Writes are what change the cluster, so this shows the story fastest. |
| **Plain English** | Arrow labels become sentences: "scheduler bound pod default/nginx-xyz to node kubescope-worker". |
| **Show** chips | Background chatter hidden by default: watches, leases, node heartbeats, auth, discovery, health checks. |
| **Lanes** chips | Hide or show a component column. Hover a lane title to read what that component does. |
| Click a row | Details: who sent it, user agent, latency, full request and response JSON. "Show only" filters to that object. |

How to read the diagram:

- Solid arrow: a request from a component to the API server. The dot is the sender.
- Dotted arrow: a watch being opened (a long-lived stream of changes).
- Dashed orange arrow to etcd: the write was stored in etcd. **Inferred** for now, real
  timing comes with API server tracing in phase 4.
- Code badge: green 2xx, orange 4xx (409 Conflict is normal: two writers raced and one retries), red 5xx.

## Guided tour

```bash
./scripts/tour.sh          # one step per component, press Enter between steps
./scripts/tour.sh --auto   # runs on its own (DELAY=12 seconds between steps)
```

Each step drops a marker in the UI and prints what to look for. Pick a step in the
**Steps** dropdown (or click its marker) to see only what that step caused:

| Step | Components you'll see |
|---|---|
| 1. kubectl reads | kubectl |
| 2. Create a namespace | kubectl, service-account and root-ca-cert-publisher controllers |
| 3. Create a deployment | kubectl, deployment + replicaset controllers, scheduler, kubelet |
| 4. Scale to 3 | replicaset controller, scheduler spreading pods, kubelets on both workers |
| 5. Expose a Service | API server allocating the ClusterIP, endpointslice controller |
| 6. Pod using DNS | scheduler, kubelet (the DNS query itself arrives in phase 3) |
| 7. Persistent volume | local-path provisioner (storage), persistent-volume-binder |
| 8. Kill a pod | kubelet, replicaset controller replacing it |
| 9. Rolling update | two ReplicaSets trading replicas |
| 10. Cordon a node | kubectl, node controller |
| 11. Delete the namespace | namespace controller, garbage collection, kubelets |

Markers can also be added from anywhere: `curl -d '{"label":"my test"}' localhost:8090/api/marker`.

## Try these

```bash
kubectl create deployment web --image=nginx      # deployment → replicaset → pod → binding → kubelet
kubectl scale deployment web --replicas=3        # replicaset controller creates 2 pods, scheduler spreads them
kubectl expose deployment web --port=80          # service → endpointslice controller
kubectl delete pod -l app=web                    # replicaset controller replaces the pods
kubectl delete deployment web                    # garbage collector deletes replicasets and pods
```

## How it works

```
kube-apiserver ──audit webhook (HTTP POST, batches)──▶ bin/kubescope ──WebSocket──▶ browser
```

- `cluster/audit-policy.yaml`: what the API server reports. Writes include bodies, reads only metadata,
  secrets and configmaps never include bodies.
- `cluster/up.sh`: renders the kind config with the address of this machine as seen from the
  cluster (the gateway of the `kind` Docker network) and creates the cluster.
- `collector/internal/audit`: converts audit events, works out which component sent each one
  (from the user and user agent) and tags noise.
- `collector/internal/store`: ring buffer of the last 20,000 events.
- `web/`: React + TypeScript UI, embedded into the Go binary at build time.

## Developing the UI

```bash
make dev                 # collector on :8090 (go run)
cd web && npm run dev    # UI with hot reload on http://localhost:5173
```

## Cleanup

```bash
make cluster-down        # deletes only the "kubescope" cluster
```
