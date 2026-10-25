import { useEffect, useRef, useState } from 'react'
import { fetchHistogram, formatDateTime, type Histogram, type TimeRange } from './history'
import type { KEvent } from './types'

const BUCKETS = 120
const H = 46

interface Props {
  win: TimeRange // what the bar shows
  live: boolean // the window follows the clock
  selection: TimeRange | null // highlighted range (the one loaded below)
  onSelect: (r: TimeRange) => void
  onMarker: (m: KEvent, next: KEvent | undefined) => void
}

// Activity per time slice. Drag to pick a range, click a blue tick to open a step.
export function Timeline({ win, live, selection, onSelect, onMarker }: Props) {
  const [hist, setHist] = useState<Histogram | null>(null)
  const [drag, setDrag] = useState<{ a: number; b: number } | null>(null)
  const [hover, setHover] = useState<number | null>(null)
  const box = useRef<HTMLDivElement>(null)

  // In live mode the parent moves the window every few seconds, which reloads this.
  useEffect(() => {
    const ctrl = new AbortController()
    fetchHistogram(win, BUCKETS, ctrl.signal)
      .then(setHist)
      .catch(() => {})
    return () => ctrl.abort()
  }, [win.from, win.to])

  const span = win.to - win.from
  const frac = (ms: number) => Math.min(1, Math.max(0, (ms - win.from) / span))
  const atX = (clientX: number) => {
    const r = box.current!.getBoundingClientRect()
    return win.from + ((clientX - r.left) / r.width) * span
  }

  const onDown = (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest('.tl-marker')) return
    const t = atX(e.clientX)
    setDrag({ a: t, b: t })
  }
  useEffect(() => {
    if (!drag) return
    const move = (e: MouseEvent) => setDrag((d) => d && { ...d, b: atX(e.clientX) })
    const up = () => {
      setDrag((d) => {
        if (d) {
          const from = Math.min(d.a, d.b)
          let to = Math.max(d.a, d.b)
          // A click without dragging opens that one slice.
          if (to - from < span / BUCKETS) to = from + span / BUCKETS
          onSelect({ from: Math.floor(from), to: Math.ceil(to) })
        }
        return null
      })
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    return () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
  }, [drag !== null])

  const buckets = hist?.buckets ?? []
  const peak = Math.max(1, ...buckets.map((b) => b.shown))
  const markers = hist?.markers ?? []
  const hovered = hover !== null ? buckets[hover] : null
  const sel = drag ? { from: Math.min(drag.a, drag.b), to: Math.max(drag.a, drag.b) } : selection

  return (
    <div className="timeline">
      <div className="tl-labels">
        <span>{formatDateTime(win.from, false)}</span>
        <span className="tl-hint">
          {hovered
            ? `${formatDateTime(new Date(hovered.time).getTime())} · ${hovered.shown} calls (${hovered.writes} writes) · ${hovered.total - hovered.shown} noise`
            : 'Drag to pick a time range · click a blue tick to open a step'}
        </span>
        <span>{live ? 'now' : formatDateTime(win.to, false)}</span>
      </div>
      <div
        className="tl-bars"
        ref={box}
        onMouseDown={onDown}
        onMouseMove={(e) => setHover(Math.floor(frac(atX(e.clientX)) * BUCKETS))}
        onMouseLeave={() => setHover(null)}
      >
        <svg viewBox={`0 0 ${BUCKETS} ${H}`} preserveAspectRatio="none" width="100%" height={H}>
          {buckets.map((b, i) => {
            const h = (b.shown / peak) * (H - 2)
            const w = (b.writes / peak) * (H - 2)
            return (
              <g key={i} className={hover === i ? 'hot' : ''}>
                <rect x={i + 0.1} y={H - h} width={0.8} height={h} className="tl-read" />
                <rect x={i + 0.1} y={H - w} width={0.8} height={w} className="tl-write" />
              </g>
            )
          })}
        </svg>
        {sel && sel.to > win.from && sel.from < win.to && (
          <div
            className="tl-selection"
            style={{ left: `${frac(sel.from) * 100}%`, width: `${Math.max(0.3, (frac(sel.to) - frac(sel.from)) * 100)}%` }}
          />
        )}
        {markers.map((m, i) => (
          <div
            key={m.id}
            className="tl-marker"
            style={{ left: `${frac(new Date(m.time).getTime()) * 100}%` }}
            title={`${formatDateTime(new Date(m.time).getTime())} · ${m.summary}`}
            onClick={() => onMarker(m, markers[i + 1])}
          />
        ))}
      </div>
    </div>
  )
}
