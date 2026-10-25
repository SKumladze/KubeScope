// Columns of the diagram, left to right. etcd sits left of the API server so
// every client arrow points left into the API server without crossing etcd.
export interface Lane {
  id: string
  label: string
  color: string
  about: string
}

export const LANES: Lane[] = [
  {
    id: 'etcd',
    label: 'etcd',
    color: '#e0a03a',
    about:
      'The key-value database that stores every object in the cluster. Only the API server talks to it. Arrows here are inferred from writes until tracing is added (phase 4).',
  },
  {
    id: 'apiserver',
    label: 'API server',
    color: '#4f8cff',
    about:
      'The front door of the cluster. Every component reads and writes state only through it. It checks auth, validates, runs admission, then saves to etcd.',
  },
  {
    id: 'kubectl',
    label: 'kubectl',
    color: '#3fc1a5',
    about: 'You. Every kubectl command becomes one or more HTTP requests to the API server.',
  },
  {
    id: 'controller-manager',
    label: 'controllers',
    color: '#b07cff',
    about:
      'Runs many controllers (deployment, replicaset, endpointslice, node...). Each watches objects and creates or updates others to match the desired state.',
  },
  {
    id: 'scheduler',
    label: 'scheduler',
    color: '#ff7aa8',
    about:
      'Watches for pods with no node, picks the best node, then writes the decision as a pods/binding request.',
  },
  {
    id: 'kubelet',
    label: 'kubelet',
    color: '#6fcf5a',
    about:
      'The agent on every node. Watches pods bound to its node, asks the container runtime to start them, and reports status back.',
  },
  {
    id: 'kube-proxy',
    label: 'kube-proxy',
    color: '#5ac8e0',
    about: 'Runs on every node. Watches Services and EndpointSlices and programs iptables so Service IPs reach pods.',
  },
  {
    id: 'coredns',
    label: 'CoreDNS',
    color: '#f0d050',
    about: 'Cluster DNS. Watches Services and answers names like my-svc.default.svc.cluster.local.',
  },
  {
    id: 'cni',
    label: 'CNI',
    color: '#ff9a5a',
    about: 'The pod network plugin (kindnet in kind). Gives pods IPs and routes between nodes.',
  },
  {
    id: 'storage',
    label: 'storage',
    color: '#a0a8b8',
    about: 'The volume provisioner (local-path in kind). Creates PersistentVolumes for claims.',
  },
  {
    id: 'probes',
    label: 'probes',
    color: '#7a8496',
    about: 'Health checks hitting the API server itself (/livez, /readyz).',
  },
  {
    id: 'other',
    label: 'other',
    color: '#8890a0',
    about: 'Anything we could not classify.',
  },
]

export const LANE_BY_ID = Object.fromEntries(LANES.map((l) => [l.id, l]))

export const NOISE = [
  { id: 'watch', label: 'watches', about: 'Long-lived streams components open to get notified of changes.' },
  { id: 'lease', label: 'leases', about: 'Leader election and node liveness, renewed every few seconds.' },
  { id: 'heartbeat', label: 'node heartbeats', about: 'Kubelets reporting node status.' },
  { id: 'auth', label: 'auth', about: 'Token and access reviews.' },
  { id: 'discovery', label: 'discovery', about: 'Clients asking which APIs exist (/api, /apis, /openapi).' },
  { id: 'health', label: 'health', about: 'Health check endpoints.' },
  { id: 'internal', label: 'API server self-checks', about: 'The API server reading its own state, e.g. the endpoints of the default "kubernetes" Service every 10 seconds.' },
]
