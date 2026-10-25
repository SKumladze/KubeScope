import { useEffect, useState } from 'react'
import type { KEvent } from './types'

export interface TimeRange {
  from: number // unix ms
  to: number
}

export interface RangeResult {
  events: KEvent[]
  total: number
  truncated: boolean
  noiseCounts: Record<string, number>
}

export interface Bucket {
  time: string
  total: number
  shown: number
  writes: number
}

export interface Histogram {
  from: string
  to: string
  bucketMs: number
  buckets: Bucket[]
  markers: KEvent[]
}

const EMPTY: RangeResult = { events: [], total: 0, truncated: false, noiseCounts: {} }

// Loads stored events for a time range. Hidden noise is filtered on the
// server so a busy hour still fits in one response.
export function useHistory(range: TimeRange | null, hideNoise: Set<string>) {
  const [result, setResult] = useState<RangeResult>(EMPTY)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const hide = [...hideNoise].sort().join(',')

  useEffect(() => {
    if (!range) {
      setResult(EMPTY)
      return
    }
    const ctrl = new AbortController()
    setLoading(true)
    setError('')
    fetch(`/api/events?from=${range.from}&to=${range.to}&hide=${hide}`, { signal: ctrl.signal })
      .then((r) => (r.ok ? r.json() : r.text().then((t) => Promise.reject(new Error(t)))))
      .then((res: RangeResult) => {
        setResult(res)
        setLoading(false)
      })
      .catch((e: Error) => {
        if (e.name === 'AbortError') return
        setError(e.message)
        setLoading(false)
      })
    return () => ctrl.abort()
  }, [range?.from, range?.to, hide])

  return { ...result, loading, error }
}

export async function fetchHistogram(win: TimeRange, buckets: number, signal?: AbortSignal): Promise<Histogram> {
  const r = await fetch(`/api/histogram?from=${Math.floor(win.from)}&to=${Math.ceil(win.to)}&buckets=${buckets}`, { signal })
  if (!r.ok) throw new Error(await r.text())
  return r.json()
}

// The view lives in the URL so a link opens the same moment:
// ?from=<ms>&to=<ms>&q=<search>&step=<marker id>&status=4xx,5xx&verbs=create&ns=default
// status and verbs list what is shown; missing means "all".
export interface UrlState {
  range: TimeRange | null
  q: string
  step: number | null
  status: string[]
  verbs: string[]
  ns: string
}

export function readUrl(): UrlState {
  const p = new URLSearchParams(location.search)
  const from = Number(p.get('from'))
  const to = Number(p.get('to'))
  const step = Number(p.get('step'))
  return {
    range: from && to && to > from ? { from, to } : null,
    q: p.get('q') ?? '',
    step: step || null,
    status: (p.get('status') ?? '').split(',').filter(Boolean),
    verbs: (p.get('verbs') ?? '').split(',').filter(Boolean),
    ns: p.get('ns') ?? '',
  }
}

export function writeUrl(s: UrlState) {
  const p = new URLSearchParams()
  if (s.range) {
    p.set('from', String(s.range.from))
    p.set('to', String(s.range.to))
  }
  if (s.q) p.set('q', s.q)
  if (s.step) p.set('step', String(s.step))
  if (s.status.length) p.set('status', s.status.join(','))
  if (s.verbs.length) p.set('verbs', s.verbs.join(','))
  if (s.ns) p.set('ns', s.ns)
  const qs = p.toString()
  history.replaceState(null, '', qs ? `?${qs}` : location.pathname)
}

export function formatDateTime(ms: number, withSeconds = true) {
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  const date = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
  const time = `${p(d.getHours())}:${p(d.getMinutes())}${withSeconds ? ':' + p(d.getSeconds()) : ''}`
  return `${date} ${time}`
}

// <input type="datetime-local"> wants local time without a zone: 2026-10-24T18:30:05
export const toLocalInput = (ms: number) => formatDateTime(ms).replace(' ', 'T')
export const fromLocalInput = (v: string) => new Date(v).getTime()
