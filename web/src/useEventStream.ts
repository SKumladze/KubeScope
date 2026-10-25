import { useEffect, useRef, useState } from 'react'
import type { KEvent, ServerMessage } from './types'

const MAX_EVENTS = 20000

export type Status = 'connecting' | 'live' | 'disconnected'

// Keeps a WebSocket open to the collector and returns all received events.
// Reconnects automatically; on reconnect the backlog replaces the list.
export function useEventStream() {
  const [events, setEvents] = useState<KEvent[]>([])
  const [status, setStatus] = useState<Status>('connecting')
  const retry = useRef<number | undefined>(undefined)

  useEffect(() => {
    let ws: WebSocket | null = null
    let stopped = false

    const connect = () => {
      setStatus('connecting')
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${location.host}/ws`)
      ws.onopen = () => setStatus('live')
      ws.onmessage = (m) => {
        const msg = JSON.parse(m.data) as ServerMessage
        if (msg.type === 'backlog') setEvents(msg.events)
        else
          setEvents((prev) => {
            const next = prev.concat(msg.events)
            return next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next
          })
      }
      ws.onclose = () => {
        setStatus('disconnected')
        if (!stopped) retry.current = window.setTimeout(connect, 1500)
      }
    }
    connect()
    return () => {
      stopped = true
      window.clearTimeout(retry.current)
      ws?.close()
    }
  }, [])

  return { events, status }
}
