import { useEffect, useState } from 'react'
import { formatDateTime } from './history'

interface Info {
  count: number
  oldest?: string
  bytes: number
  retentionHours: number
  maxBytes: number
  noisePolicy: string
  sizeLimited: boolean
}

// Shows how far back history goes and how full the database is, so nobody
// is surprised when an old time range comes back empty.
export function HistoryInfo() {
  const [info, setInfo] = useState<Info | null>(null)

  useEffect(() => {
    const load = () =>
      fetch('/api/info')
        .then((r) => (r.ok ? r.json() : null))
        .then(setInfo)
        .catch(() => {})
    load()
    const t = window.setInterval(load, 30_000)
    return () => window.clearInterval(t)
  }, [])

  if (!info) return null
  const since = info.oldest ? formatDateTime(new Date(info.oldest).getTime(), false) : 'empty'
  const limit = info.maxBytes ? ` of ${formatBytes(info.maxBytes)}` : ''
  const keptHours = info.oldest ? (Date.now() - new Date(info.oldest).getTime()) / 3_600_000 : 0

  const title = [
    `${info.count.toLocaleString()} events stored`,
    `Kept for ${formatHours(info.retentionHours)}${info.maxBytes ? ` or until the file reaches ${formatBytes(info.maxBytes)}` : ''}, whichever comes first`,
    `Noise stored: ${info.noisePolicy}`,
    info.sizeLimited
      ? `The size limit is deleting history early: only ${formatHours(keptHours)} kept instead of ${formatHours(info.retentionHours)}. Raise -max-db-mb or store less noise.`
      : '',
  ]
    .filter(Boolean)
    .join('\n')

  return (
    <span className={`history-info${info.sizeLimited ? ' warn' : ''}`} title={title}>
      {info.sizeLimited ? '⚠ ' : ''}History since {since} · {formatBytes(info.bytes)}
      {limit}
    </span>
  )
}

function formatBytes(b: number) {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`
  if (b >= 1 << 20) return `${Math.round(b / (1 << 20))} MB`
  return `${Math.round(b / 1024)} KB`
}

function formatHours(h: number) {
  if (!h) return 'forever'
  if (h < 1) return `${Math.max(1, Math.round(h * 60))} minutes`
  return h >= 48 ? `${Math.round(h / 24)} days` : `${Math.round(h)} hours`
}
