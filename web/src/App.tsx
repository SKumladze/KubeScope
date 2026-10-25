import { useEffect, useMemo, useState } from 'react'
import { Diagram } from './Diagram'
import { DetailPanel } from './DetailPanel'
import { HistoryInfo } from './HistoryInfo'
import { LANES, NOISE } from './lanes'
import { Timeline } from './Timeline'
import { formatDateTime, fromLocalInput, readUrl, toLocalInput, useHistory, writeUrl, type TimeRange } from './history'
import { SEARCH_HELP, STATUS_CLASSES, VERBS, parseSearch, statusClass, verbGroup } from './filter'
import { isMarker, type KEvent } from './types'
import { useEventStream } from './useEventStream'

const MIN = 60_000
const PRESETS = [
  { id: '15m', label: '15m', ms: 15 * MIN },
  { id: '1h', label: '1h', ms: 60 * MIN },
  { id: '6h', label: '6h', ms: 360 * MIN },
  { id: '24h', label: '24h', ms: 1440 * MIN },
  { id: '7d', label: '7d', ms: 7 * 1440 * MIN },
]
const LIVE_WINDOW = 60 * MIN

export function App() {
  const initial = useMemo(readUrl, [])
  const live = useEventStream()

  // null = live; otherwise the stored time range shown in the diagram.
  const [range, setRange] = useState<TimeRange | null>(initial.range)
  // What the activity bar shows. In live mode it follows the clock.
  const [win, setWin] = useState<TimeRange>(() => initial.range ?? lastWindow(LIVE_WINDOW))
  const [preset, setPreset] = useState<string>(initial.range ? 'custom' : 'live')

  const [hiddenNoise, setHiddenNoise] = useState<Set<string>>(() => new Set(NOISE.map((n) => n.id)))
  const [hiddenLanes, setHiddenLanes] = useState<Set<string>>(() => new Set())
  const [writesOnly, setWritesOnly] = useState(false)
  // Status classes and verbs to show; an empty set means "all".
  const [statusOnly, setStatusOnly] = useState<Set<string>>(() => new Set(initial.status))
  const [verbsOnly, setVerbsOnly] = useState<Set<string>>(() => new Set(initial.verbs))
  const [ns, setNs] = useState(initial.ns)
  const [search, setSearch] = useState(initial.q)
  const [plain, setPlain] = useState(false)
  const [pausedAt, setPausedAt] = useState<number | null>(null)
  const [clearedAt, setClearedAt] = useState(0)
  const [follow, setFollow] = useState(!initial.range)
  const [focusId, setFocusId] = useState<number | null>(initial.step)
  const [selected, setSelected] = useState<KEvent | null>(null)

  const hist = useHistory(range, hiddenNoise)
  const isLive = range === null

  useEffect(() => {
    if (!isLive) return
    const t = window.setInterval(() => setWin(lastWindow(LIVE_WINDOW)), 5000)
    return () => window.clearInterval(t)
  }, [isLive])

  useEffect(
    () => writeUrl({ range, q: search, step: focusId, status: [...statusOnly], verbs: [...verbsOnly], ns }),
    [range, search, focusId, statusOnly, verbsOnly, ns],
  )

  const events = isLive ? live.events : hist.events
  const lastId = live.events.length ? live.events[live.events.length - 1].id : 0
  const newWhilePaused = pausedAt === null ? 0 : live.events.filter((e) => e.id > pausedAt).length

  const noiseCounts = useMemo(() => {
    if (!isLive) return hist.noiseCounts
    const c: Record<string, number> = {}
    for (const e of events) if (e.noise && e.id > clearedAt) c[e.noise] = (c[e.noise] ?? 0) + 1
    return c
  }, [isLive, events, clearedAt, hist.noiseCounts])

  const markers = useMemo(() => events.filter((e) => isMarker(e) && e.id > clearedAt), [events, clearedAt])

  // Focusing a marker shows only the events between it and the next marker.
  const focus = useMemo(() => {
    const i = markers.findIndex((m) => m.id === focusId)
    if (i < 0) return null
    return { marker: markers[i], end: markers[i + 1]?.id ?? Infinity }
  }, [markers, focusId])

  // base: what the time window, step, noise and lane settings allow.
  // The status/verb/namespace counts are taken from it, so they show what each filter would add.
  const base = useMemo(
    () =>
      events.filter((e) => {
        if (isLive && (e.id <= clearedAt || (pausedAt !== null && e.id > pausedAt))) return false
        if (focus && (e.id < focus.marker.id || e.id >= focus.end)) return false
        if (isMarker(e)) return true
        if (e.noise && hiddenNoise.has(e.noise)) return false
        return !hiddenLanes.has(e.from)
      }),
    [events, isLive, clearedAt, pausedAt, focus, hiddenNoise, hiddenLanes],
  )

  const facets = useMemo(() => {
    const status: Record<string, number> = {}
    const verbs: Record<string, number> = {}
    const namespaces: Record<string, number> = {}
    for (const e of base) {
      if (isMarker(e)) continue
      const sc = statusClass(e.code)
      status[sc] = (status[sc] ?? 0) + 1
      const v = verbGroup(e.verb)
      verbs[v] = (verbs[v] ?? 0) + 1
      if (e.namespace) namespaces[e.namespace] = (namespaces[e.namespace] ?? 0) + 1
    }
    return { status, verbs, namespaces: Object.entries(namespaces).sort((a, b) => a[0].localeCompare(b[0])) }
  }, [base])

  const filtered = useMemo(() => {
    const checks = parseSearch(search)
    return base.filter((e) => {
      if (isMarker(e)) return true
      if (statusOnly.size && !statusOnly.has(statusClass(e.code))) return false
      if (verbsOnly.size && !verbsOnly.has(verbGroup(e.verb))) return false
      if (ns && e.namespace !== ns) return false
      if (writesOnly && !e.write) return false
      return checks.every((c) => c(e))
    })
  }, [base, statusOnly, verbsOnly, ns, writesOnly, search])

  // Counts are API calls only: marker rows are not counted.
  const filteredMarkers = useMemo(() => filtered.filter(isMarker).length, [filtered])
  const total = isLive
    ? events.filter((e) => e.id > clearedAt).length - markers.length
    : hist.total - markers.length

  const filtersOn = statusOnly.size > 0 || verbsOnly.size > 0 || ns !== '' || writesOnly || search !== ''
  const resetFilters = () => {
    setStatusOnly(new Set())
    setVerbsOnly(new Set())
    setNs('')
    setWritesOnly(false)
    setSearch('')
  }

  const laneCounts = useMemo(() => {
    const c: Record<string, number> = {}
    for (const e of filtered) c[e.from] = (c[e.from] ?? 0) + 1
    return c
  }, [filtered])

  const lanes = LANES.filter((l) => !hiddenLanes.has(l.id))

  const toggle = (set: Set<string>, id: string, update: (s: Set<string>) => void) => {
    const next = new Set(set)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    update(next)
  }

  const goLive = () => {
    setRange(null)
    setPreset('live')
    setWin(lastWindow(LIVE_WINDOW))
    setFocusId(null)
    setFollow(true)
  }

  const showRange = (r: TimeRange, opts: { preset?: string; zoom?: boolean; step?: number | null } = {}) => {
    setRange(r)
    setPreset(opts.preset ?? 'custom')
    if (opts.zoom) setWin(r)
    setFocusId(opts.step ?? null)
    setPausedAt(null)
    setSelected(null)
    setFollow(false)
  }

  const pickPreset = (id: string) => {
    const p = PRESETS.find((x) => x.id === id)
    if (!p) return goLive()
    showRange(lastWindow(p.ms), { preset: id, zoom: true })
  }

  const addMarker = () => {
    // Named by time, so names stay unique after "Clear screen".
    fetch('/api/marker', { method: 'POST', body: JSON.stringify({ label: `Marker ${new Date().toLocaleTimeString()}` }) })
    setFollow(true)
  }

  const [copied, setCopied] = useState(false)
  const copyLink = async () => {
    // A live view has no fixed moment; share the last 15 minutes instead.
    const url = new URL(location.href)
    if (isLive) {
      const r = lastWindow(15 * MIN)
      url.searchParams.set('from', String(r.from))
      url.searchParams.set('to', String(r.to))
    }
    await navigator.clipboard.writeText(url.toString())
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className="app">
      <header className="toolbar">
        <div className="brand">
          <span className="logo">◎</span> KubeScope
          <span className={`status ${isLive ? live.status : 'history'}`}>{isLive ? live.status : 'history'}</span>
        </div>

        <div className="time-controls">
          <div className="segmented">
            <button className={preset === 'live' ? 'active' : ''} onClick={goLive}>
              ● Live
            </button>
            {PRESETS.map((p) => (
              <button key={p.id} className={preset === p.id ? 'active' : ''} onClick={() => pickPreset(p.id)}>
                {p.label}
              </button>
            ))}
          </div>
          {range && (
            <div className="custom-range">
              <input
                type="datetime-local"
                step={1}
                value={toLocalInput(range.from)}
                onChange={(e) => {
                  const from = fromLocalInput(e.target.value)
                  if (from < range.to) showRange({ from, to: range.to }, { zoom: true })
                }}
              />
              →
              <input
                type="datetime-local"
                step={1}
                value={toLocalInput(range.to)}
                onChange={(e) => {
                  const to = fromLocalInput(e.target.value)
                  if (to > range.from) showRange({ from: range.from, to }, { zoom: true })
                }}
              />
              {(range.from > win.from || range.to < win.to) && (
                <button onClick={() => setWin(range)} title="Make the activity bar show only this range">
                  Zoom in
                </button>
              )}
            </div>
          )}
          <button onClick={copyLink} title="Copy a link to exactly this view">
            {copied ? 'Copied!' : 'Copy link'}
          </button>
          <HistoryInfo />
        </div>

        <Timeline
          win={win}
          live={isLive}
          selection={range}
          onSelect={(r) => showRange(r)}
          onMarker={(m, next) => {
            const from = new Date(m.time).getTime()
            const to = next ? new Date(next.time).getTime() : Math.max(win.to, from + MIN)
            showRange({ from, to }, { step: m.id })
          }}
        />

        <div className="controls">
          {isLive && (
            <>
              <button className={pausedAt !== null ? 'active' : ''} onClick={() => setPausedAt(pausedAt === null ? lastId : null)}>
                {pausedAt === null ? 'Pause' : `Resume${newWhilePaused ? ` (+${newWhilePaused})` : ''}`}
              </button>
              <button onClick={addMarker} title="Draw a line here, then run a command to see what it caused">
                Add marker
              </button>
              <button
                onClick={() => {
                  setClearedAt(lastId)
                  setFocusId(null)
                  setSelected(null)
                }}
                title="Clears the screen only. Everything stays in history."
              >
                Clear screen
              </button>
            </>
          )}
          <input
            type="search"
            placeholder="Search: nginx, code:409, verb:create, ms>100…"
            title={SEARCH_HELP}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <select value={ns} onChange={(e) => setNs(e.target.value)} className={ns ? 'active' : ''} title="Namespace">
            <option value="">All namespaces</option>
            {facets.namespaces.map(([n, c]) => (
              <option key={n} value={n}>
                {n} ({c})
              </option>
            ))}
            {ns && !facets.namespaces.some(([n]) => n === ns) && <option value={ns}>{ns} (0)</option>}
          </select>
          <label className="check">
            <input type="checkbox" checked={writesOnly} onChange={(e) => setWritesOnly(e.target.checked)} />
            Writes only
          </label>
          <label className="check">
            <input type="checkbox" checked={plain} onChange={(e) => setPlain(e.target.checked)} />
            Plain English
          </label>
          {markers.length > 0 && (
            <select
              className={focus ? 'active' : ''}
              value={focusId ?? ''}
              onChange={(e) => {
                setFocusId(e.target.value ? Number(e.target.value) : null)
                setFollow(isLive && !e.target.value)
              }}
              title="Show only what happened between one marker and the next"
            >
              <option value="">All steps ({markers.length} markers)</option>
              {markers.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.summary}
                </option>
              ))}
            </select>
          )}
          <span className="count">
            {(filtered.length - filteredMarkers).toLocaleString()} / {total.toLocaleString()}
            {hist.loading && ' · loading…'}
          </span>
          {filtersOn && (
            <button onClick={resetFilters} title="Clear status, verb, namespace, writes-only and search filters">
              Reset filters
            </button>
          )}
        </div>

        <div className="chips">
          <span className="chips-title">Show:</span>
          {NOISE.map((n) => (
            <button
              key={n.id}
              className={`chip${hiddenNoise.has(n.id) ? '' : ' on'}`}
              title={n.about}
              onClick={() => toggle(hiddenNoise, n.id, setHiddenNoise)}
            >
              {n.label} <small>{noiseCounts[n.id] ?? 0}</small>
            </button>
          ))}
        </div>

        <div className="chips">
          <span className="chips-title">Status:</span>
          {STATUS_CLASSES.map((c) => (
            <button
              key={c.id}
              className={`chip status s${c.id[0]}${statusOnly.has(c.id) ? ' on' : ''}`}
              title={`${c.about}. Click to show only these, click more to combine.`}
              onClick={() => toggle(statusOnly, c.id, setStatusOnly)}
            >
              {c.label} <small>{facets.status[c.id] ?? 0}</small>
            </button>
          ))}
          <span className="chips-title">Verbs:</span>
          {VERBS.map((v) => (
            <button
              key={v.id}
              className={`chip${verbsOnly.has(v.id) ? ' on' : ''}`}
              title={`${v.about}. Click to show only these, click more to combine.`}
              onClick={() => toggle(verbsOnly, v.id, setVerbsOnly)}
            >
              {v.id} <small>{facets.verbs[v.id] ?? 0}</small>
            </button>
          ))}
          <span className="chips-title">Lanes:</span>
          {LANES.filter((l) => l.id !== 'apiserver' && l.id !== 'etcd').map((l) => (
            <button
              key={l.id}
              className={`chip lane${hiddenLanes.has(l.id) ? '' : ' on'}`}
              style={{ ['--c' as string]: l.color }}
              title={l.about}
              onClick={() => toggle(hiddenLanes, l.id, setHiddenLanes)}
            >
              {l.label}
            </button>
          ))}
        </div>
      </header>

      {!isLive && (hist.truncated || hist.error) && (
        <div className={`banner${hist.error ? ' error' : ''}`}>
          {hist.error
            ? `Could not load history: ${hist.error}`
            : `Showing the first ${hist.events.length.toLocaleString()} of ${hist.total.toLocaleString()} events from ${formatDateTime(range!.from)}. Drag a smaller range on the activity bar to see the rest.`}
        </div>
      )}

      <main>
        <Diagram
          rows={filtered}
          focusId={focusId}
          onMarkerClick={(m) => {
            setFocusId(focusId === m.id ? null : m.id)
            setFollow(false)
          }}
          lanes={lanes}
          counts={laneCounts}
          selectedId={selected?.id ?? null}
          onSelect={setSelected}
          plain={plain}
          follow={follow}
          onFollowChange={setFollow}
          scrollKey={`${range?.from}-${range?.to}-${focusId}`}
          emptyText={
            isLive
              ? 'No events match. Run a kubectl command, or show hidden categories in the toolbar.'
              : hist.loading
                ? 'Loading…'
                : 'Nothing stored in this time range matches. Try a wider range or show hidden categories.'
          }
        />
        {isLive && !follow && !focus && filtered.length > 0 && (
          <button className="jump" onClick={() => setFollow(true)}>
            ↓ Jump to latest
          </button>
        )}
        {selected && !isMarker(selected) && (
          <DetailPanel event={selected} onClose={() => setSelected(null)} onFocus={setSearch} />
        )}
      </main>
    </div>
  )
}

function lastWindow(ms: number): TimeRange {
  const to = Date.now()
  return { from: to - ms, to }
}
