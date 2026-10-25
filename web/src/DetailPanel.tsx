import { useEffect, useState } from 'react'
import { LANE_BY_ID } from './lanes'
import type { KDetail, KEvent } from './types'
import { formatTime } from './Diagram'

interface Props {
  event: KEvent
  onClose: () => void
  onFocus: (text: string) => void
}

export function DetailPanel({ event, onClose, onFocus }: Props) {
  const [detail, setDetail] = useState<KDetail | null>(null)
  const [error, setError] = useState('')
  const [notStored, setNotStored] = useState(false)

  useEffect(() => {
    setDetail(null)
    setError('')
    setNotStored(false)
    fetch(`/api/events/${event.id}`)
      .then((r) => {
        if (r.status === 404) {
          setNotStored(true)
          return null
        }
        return r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))
      })
      .then((d) => d && setDetail(d))
      .catch((e: Error) => setError(e.message))
  }, [event.id])

  const lane = LANE_BY_ID[event.from]
  const rows: [string, string | number | undefined][] = [
    ['Time', formatTime(event.time)],
    ['Component', event.fromInstance ? `${event.from} (${event.fromInstance})` : event.from],
    ['User', event.user],
    ['User agent', event.userAgent],
    ['Source IP', event.sourceIp],
    ['Verb', `${event.verb}  (HTTP ${event.method})`],
    ['URI', event.uri],
    ['API', [event.apiGroup || 'core', event.apiVersion].filter(Boolean).join('/')],
    ['Resource', event.subresource ? `${event.resource}/${event.subresource}` : event.resource],
    ['Namespace', event.namespace],
    ['Name', event.name],
    ['UID', event.uid],
    ['Response', event.code],
    ['Latency', `${event.latencyMs.toFixed(1)} ms`],
    ['Audit ID', event.auditId],
  ]

  return (
    <aside className="detail">
      <header>
        <h2 style={{ color: lane?.color }}>{event.summary}</h2>
        <button className="close" onClick={onClose} aria-label="Close">
          ×
        </button>
      </header>

      {lane && <p className="about">{lane.about}</p>}
      {event.write && event.code < 400 && (
        <p className="about">
          This is a write, so the API server stored the new state in etcd before answering. Every component
          watching <b>{event.resource}</b> is now notified.
        </p>
      )}

      {event.name && (
        <div className="focus-buttons">
          <button onClick={() => onFocus(event.name!)}>Show only “{event.name}”</button>
          {event.uid && <button onClick={() => onFocus(event.uid!)}>Show this UID</button>}
        </div>
      )}

      <table>
        <tbody>
          {rows
            .filter(([, v]) => v !== undefined && v !== '')
            .map(([k, v]) => (
              <tr key={k}>
                <th>{k}</th>
                <td>{v}</td>
              </tr>
            ))}
        </tbody>
      </table>

      {error && <p className="error">Could not load bodies: {error}</p>}
      {notStored && (
        <p className="muted">
          {event.noise
            ? `Not saved in history: "${event.noise}" is background noise, shown live only. Everything known about it is in the table above.`
            : 'No longer in history: deleted by the retention or size limit.'}
        </p>
      )}
      {!detail && !error && !notStored && <p className="muted">Loading…</p>}
      {detail && !event.hasBody && (
        <p className="muted">
          No bodies recorded. The audit policy logs reads at Metadata level and sensitive objects without bodies.
        </p>
      )}
      {detail?.requestObject !== undefined && <Json title="Request body" value={detail.requestObject} />}
      {detail?.responseObject !== undefined && <Json title="Response body" value={detail.responseObject} />}
      {detail?.annotations && <Json title="Annotations" value={detail.annotations} open={false} />}
    </aside>
  )
}

function Json({ title, value, open = true }: { title: string; value: unknown; open?: boolean }) {
  return (
    <details open={open}>
      <summary>{title}</summary>
      <pre>{JSON.stringify(value, null, 2)}</pre>
    </details>
  )
}
