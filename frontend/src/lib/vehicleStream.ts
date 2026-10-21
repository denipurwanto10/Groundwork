import { vehicleStreamUrl } from './api'
import { getToken } from './session'
import type { VehicleFrame } from './types'

/** Connection state of the live feed, as shown in the vehicle panel. */
export type StreamState = 'connecting' | 'live' | 'reconnecting'

export interface StreamHandlers {
  /**
   * Every accepted frame: a full snapshot first, then one position each, plus
   * route frames so a route set in another tab shows up here without a reload.
   * A geofence frame reports that a unit crossed a zone boundary.
   */
  onFrame: (frame: VehicleFrame) => void
  onState: (state: StreamState) => void
}

const FIRST_RETRY_MS = 1000
const MAX_RETRY_MS = 15000

/** Normal closure: the socket says goodbye instead of being yanked. */
const CLOSE_NORMAL = 1000

/**
 * Closes a socket without aborting a handshake that is still in progress.
 *
 * `ws.close()` while CONNECTING tears the TCP connection mid-upgrade. The Go
 * server has already queued its first snapshot by then, so the Vite WS proxy
 * tries to write that frame into a socket the browser dropped and logs
 * `write ECONNABORTED`. Waiting for `open` and then closing with code 1000
 * turns that into an ordinary close handshake — the root-cause fix rather than
 * a muted log line.
 */
function closeCleanly(ws: WebSocket, reason: string) {
  if (ws.readyState === WebSocket.OPEN) {
    ws.close(CLOSE_NORMAL, reason)
    return
  }
  if (ws.readyState === WebSocket.CONNECTING) {
    ws.addEventListener('open', () => ws.close(CLOSE_NORMAL, reason), { once: true })
    // A handshake that fails never fires open; make sure the socket is released.
    ws.addEventListener('error', () => ws.close(), { once: true })
  }
}

/**
 * Opens the live vehicle feed and reconnects on its own with exponential
 * backoff. Returns a closer for the React effect that owns the connection.
 *
 * The backend drops a subscriber that cannot keep up, and a laptop that
 * suspends kills the socket without a close event we can trust, so the
 * reconnect lives here rather than in a one-shot connect call.
 */
export function openVehicleStream({ onFrame, onState }: StreamHandlers): () => void {
  let socket: WebSocket | null = null
  let retryTimer: number | null = null
  let retryMs = FIRST_RETRY_MS
  let attempts = 0
  let closed = false

  const setState = (state: StreamState) => {
    // After the owning effect cleaned up, a late open/close must not flip the
    // panel back to "live" or schedule another connection.
    if (closed) return
    onState(state)
  }

  const scheduleRetry = () => {
    if (closed) return
    setState('reconnecting')
    retryTimer = window.setTimeout(() => {
      retryTimer = null
      connect()
    }, retryMs)
    retryMs = Math.min(retryMs * 2, MAX_RETRY_MS)
  }

  function connect() {
    if (closed) return
    setState(attempts === 0 ? 'connecting' : 'reconnecting')

    let ws: WebSocket
    try {
      // The JWT travels as the first subprotocol so it never lands in a URL or
      // an access log; the server echoes it back to complete the handshake.
      const token = getToken()
      ws = token ? new WebSocket(vehicleStreamUrl(), [token]) : new WebSocket(vehicleStreamUrl())
    } catch {
      attempts += 1
      scheduleRetry()
      return
    }
    socket = ws

    ws.onopen = () => {
      if (closed) {
        // Cleanup ran while the handshake was in flight: close normally.
        closeCleanly(ws, 'client closing')
        return
      }
      attempts = 0
      retryMs = FIRST_RETRY_MS
      setState('live')
    }

    ws.onmessage = (ev: MessageEvent) => {
      if (closed || typeof ev.data !== 'string') return
      let frame: VehicleFrame
      try {
        frame = JSON.parse(ev.data) as VehicleFrame
      } catch {
        return
      }
      if (!frame || typeof frame.type !== 'string') return
      onFrame(frame)
    }

    // A failed socket always reports onclose right after onerror, so the
    // retry (and the state change) is scheduled in one place only.
    ws.onerror = () => {
      if (ws.readyState !== WebSocket.CLOSED) ws.close()
    }

    ws.onclose = () => {
      if (socket === ws) socket = null
      attempts += 1
      scheduleRetry()
    }
  }

  connect()

  return () => {
    closed = true
    if (retryTimer !== null) window.clearTimeout(retryTimer)
    retryTimer = null

    const open = socket
    socket = null
    if (open) {
      // Detach first: a close we caused must never schedule a reconnect.
      open.onmessage = null
      open.onerror = null
      open.onclose = null
      closeCleanly(open, 'client closing')
    }
  }
}
