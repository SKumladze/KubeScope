// Mirrors collector/internal/model/event.go
export interface KEvent {
  id: number
  time: string
  source: string
  auditId?: string
  stage?: string
  from: string
  fromInstance?: string
  to: string
  user?: string
  userAgent?: string
  sourceIp?: string
  verb: string
  method?: string
  apiGroup?: string
  apiVersion?: string
  resource?: string
  subresource?: string
  namespace?: string
  name?: string
  uid?: string
  uri?: string
  code: number
  latencyMs: number
  write: boolean
  noise?: string
  summary: string
  hasBody: boolean
}

export interface KDetail extends KEvent {
  requestObject?: unknown
  responseObject?: unknown
  responseStatus?: unknown
  annotations?: Record<string, string>
}

// Markers are dividers stored on the server like events (source "marker"),
// added by the "Add marker" button or the tour script.
export const isMarker = (e: KEvent) => e.source === 'marker'

export type ServerMessage =
  | { type: 'backlog'; events: KEvent[] }
  | { type: 'events'; events: KEvent[] }
