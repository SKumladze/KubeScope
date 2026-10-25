import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { LANE_BY_ID, type Lane } from './lanes'
import { isMarker, type KEvent } from './types'

const ROW_H = 30
const OVERSCAN = 12

interface Props {
  rows: KEvent[]
  focusId: number | null
  onMarkerClick: (m: KEvent) => void
  lanes: Lane[]
  counts: Record<string, number>
  selectedId: number | null
  onSelect: (e: KEvent) => void
  plain: boolean
  follow: boolean
  onFollowChange: (f: boolean) => void
  emptyText: string
  // When this changes (new time range or step), jump back to the top.
  scrollKey: string
}

// A sequence diagram: lanes are columns, time goes down, one arrow per API call.
// Rows are virtualized so tens of thousands of events stay smooth.
export function Diagram({ rows, focusId, onMarkerClick, lanes, counts, selectedId, onSelect, plain, follow, onFollowChange, emptyText, scrollKey }: Props) {
  const scroller = useRef<HTMLDivElement>(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [viewH, setViewH] = useState(600)

  useEffect(() => {
    const el = scroller.current
    if (!el) return
    const ro = new ResizeObserver(() => setViewH(el.clientHeight))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  useLayoutEffect(() => {
    const el = scroller.current
    if (follow && el) el.scrollTop = el.scrollHeight
  }, [rows, follow])

  useLayoutEffect(() => {
    const el = scroller.current
    if (el && !follow) {
      el.scrollTop = 0
      setScrollTop(0)
    }
  }, [scrollKey])

  const onScroll = () => {
    const el = scroller.current!
    setScrollTop(el.scrollTop)
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < ROW_H * 2
    if (atBottom !== follow) onFollowChange(atBottom)
  }

  const index = Object.fromEntries(lanes.map((l, i) => [l.id, i]))
  const x = (laneId: string) => {
    const i = index[laneId] ?? index['other']
    return i === undefined ? -1 : ((i + 0.5) / lanes.length) * 100
  }

  const first = Math.max(0, Math.floor(scrollTop / ROW_H) - OVERSCAN)
  const last = Math.min(rows.length, Math.ceil((scrollTop + viewH) / ROW_H) + OVERSCAN)
  const visible = rows.slice(first, last)

  return (
    <div className="diagram" ref={scroller} onScroll={onScroll}>
      <div className="lane-header">
        <div className="time-col">time</div>
        <div className="lanes-area">
          {lanes.map((l) => (
            <div key={l.id} className="lane-title" title={l.about} style={{ borderColor: l.color }}>
              <span style={{ color: l.color }}>{l.label}</span>
              <small>{counts[l.id] ?? 0}</small>
            </div>
          ))}
        </div>
      </div>

      <div className="rows" style={{ height: rows.length * ROW_H }}>
        <div className="lifelines">
          {lanes.map((l) => (
            <div key={l.id} className="lifeline" style={{ left: `${x(l.id)}%`, borderColor: l.color }} />
          ))}
        </div>

        {visible.map((r, i) => {
          const top = (first + i) * ROW_H
          if (isMarker(r)) {
            return (
              <div
                key={r.id}
                className={`marker-row${r.id === focusId ? ' focused' : ''}`}
                style={{ top }}
                onClick={() => onMarkerClick(r)}
                title={r.id === focusId ? 'Click to show all steps' : 'Click to show only this step'}
              >
                <span>
                  {formatTime(r.time)} · {r.summary}
                </span>
              </div>
            )
          }
          return (
            <EventRow
              key={r.id}
              e={r}
              top={top}
              x={x}
              selected={r.id === selectedId}
              plain={plain}
              onSelect={onSelect}
            />
          )
        })}
      </div>

      {rows.length === 0 && (
        <div className="empty">{emptyText}</div>
      )}
    </div>
  )
}

function EventRow({
  e,
  top,
  x,
  selected,
  plain,
  onSelect,
}: {
  e: KEvent
  top: number
  x: (lane: string) => number
  selected: boolean
  plain: boolean
  onSelect: (e: KEvent) => void
}) {
  const color = LANE_BY_ID[e.from]?.color ?? '#888'
  const from = x(e.from)
  const to = x(e.to)
  const etcd = x('etcd')
  const left = Math.min(from, to)
  const width = Math.abs(from - to)
  const failed = e.code >= 400
  const stored = e.write && !failed && etcd >= 0 && to >= 0

  const target = [e.namespace, e.name].filter(Boolean).join('/')
  const res = e.subresource ? `${e.resource}/${e.subresource}` : e.resource || e.uri
  const short = `${e.method ?? e.verb} ${res}${target ? ' ' + target : ''}`
  const who = e.fromInstance ? `${e.fromInstance} · ` : ''
  const label = plain ? e.summary : who + short

  return (
    <div
      className={`row${selected ? ' selected' : ''}${e.noise ? ' noisy' : ''}`}
      style={{ top }}
      onClick={() => onSelect(e)}
      title={e.summary}
    >
      <div className="time-col">{formatTime(e.time)}</div>
      <div className="lanes-area">
        {stored && (
          <div className="arrow inferred" style={{ left: `${etcd}%`, width: `${to - etcd}%` }}>
            <div className="line" />
            <div className="head left" />
          </div>
        )}
        {e.from === e.to ? (
          <div className="self-call" style={{ left: `${to}%`, borderColor: color }} />
        ) : (
          <div
            className={`arrow${e.verb === 'watch' ? ' watch' : ''}`}
            style={{ left: `${left}%`, width: `${width}%`, color }}
          >
            <div className="line" />
            <div className={`head ${to < from ? 'left' : 'right'}`} />
            <div className="dot" style={{ [to < from ? 'right' : 'left']: -4 }} />
          </div>
        )}
        <div className="label" style={{ left: `calc(${Math.min(from, to)}% + 10px)` }}>
          <span className={`code ${codeClass(e.code)}`}>{e.code || '…'}</span>
          <span className={`text${e.write ? ' write' : ''}`}>{label}</span>
        </div>
      </div>
    </div>
  )
}

function codeClass(code: number) {
  if (!code) return 'pending'
  if (code >= 500) return 'err'
  if (code >= 400) return 'warn'
  return 'ok'
}

export function formatTime(iso: string) {
  const d = new Date(iso)
  const p = (n: number, w = 2) => String(n).padStart(w, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`
}
