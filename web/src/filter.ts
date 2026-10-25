import type { KEvent } from './types'

export const STATUS_CLASSES = [
  { id: '1xx', label: '1xx', about: 'Protocol switch: exec, attach and port-forward upgrade the connection (101 Switching Protocols)' },
  { id: '2xx', label: '2xx', about: 'Success: 200 OK, 201 Created, 202 Accepted' },
  { id: '3xx', label: '3xx', about: 'Redirects (rare in Kubernetes)' },
  { id: '4xx', label: '4xx', about: 'Client errors: 400, 401, 403, 404, 405, 409 Conflict, 415, 422' },
  { id: '5xx', label: '5xx', about: 'Server errors: the API server or something behind it failed' },
]

export const VERBS = [
  { id: 'get', about: 'Read one object (HTTP GET /pods/name)' },
  { id: 'list', about: 'Read many objects (HTTP GET /pods)' },
  { id: 'watch', about: 'Open a stream of changes (HTTP GET ?watch=true)' },
  { id: 'create', about: 'Create an object (HTTP POST)' },
  { id: 'update', about: 'Replace an object (HTTP PUT)' },
  { id: 'patch', about: 'Change part of an object (HTTP PATCH)' },
  { id: 'delete', about: 'Delete one or many objects (HTTP DELETE)' },
]

export const statusClass = (code: number) => (code ? `${Math.floor(code / 100)}xx` : '')

// "deletecollection" is grouped with "delete" in the chips.
export const verbGroup = (verb: string) => (verb === 'deletecollection' ? 'delete' : verb)

export const SEARCH_HELP =
  'Words match anywhere. Fields: code:409 code:4xx verb:create ns:default res:pods name:web ' +
  'from:scheduler node:kubescope-worker user:admin. Latency: ms>100 ms<5'

type Check = (e: KEvent) => boolean

// parseSearch turns "verb:create ns:default nginx ms>100" into checks that must all pass.
export function parseSearch(q: string): Check[] {
  const checks: Check[] = []
  for (const tok of q.toLowerCase().split(/\s+/).filter(Boolean)) {
    const latency = /^ms([<>])(\d+(?:\.\d+)?)$/.exec(tok)
    if (latency) {
      const n = Number(latency[2])
      checks.push(latency[1] === '>' ? (e) => e.latencyMs > n : (e) => e.latencyMs < n)
      continue
    }
    const field = /^(\w+):(.+)$/.exec(tok)
    if (field) {
      const [, key, val] = field
      const has = (v: string | undefined) => (v ?? '').toLowerCase().includes(val)
      const is = (v: string | undefined) => (v ?? '').toLowerCase() === val
      switch (key) {
        case 'code':
          checks.push(/^\dxx$/.test(val) ? (e) => statusClass(e.code) === val : (e) => String(e.code) === val)
          continue
        case 'verb':
          checks.push((e) => is(e.verb) || is(e.method))
          continue
        case 'ns':
        case 'namespace':
          checks.push((e) => is(e.namespace))
          continue
        case 'res':
        case 'resource':
          checks.push((e) => has(e.resource) || has(e.subresource))
          continue
        case 'name':
          checks.push((e) => has(e.name))
          continue
        case 'from':
        case 'lane':
          checks.push((e) => has(e.from) || has(e.fromInstance))
          continue
        case 'node':
          checks.push((e) => e.from === 'kubelet' && has(e.fromInstance))
          continue
        case 'user':
          checks.push((e) => has(e.user))
          continue
      }
    }
    checks.push((e) =>
      [e.summary, e.name, e.namespace, e.resource, e.from, e.fromInstance, e.user, e.uid, e.verb, String(e.code)]
        .join(' ')
        .toLowerCase()
        .includes(tok),
    )
  }
  return checks
}
