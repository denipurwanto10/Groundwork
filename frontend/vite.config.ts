import react from '@vitejs/plugin-react'
import { defineConfig, type ProxyOptions } from 'vite'

// https://vite.dev/config/
// Dev proxy: backend Gin has no CORS, so /api and /health are forwarded to the
// Go server. The port is configurable because this machine can have an older
// build still listening; `ws: true` is what lets the /api/vehicles/stream
// WebSocket upgrade through the dev server.
// In production set VITE_API_BASE_URL to the backend instead.
// The backend binds to the loopback address (SERVER_HOST), so the proxy
// talks to 127.0.0.1: "localhost" can resolve to ::1 first, and a server
// that only listens on IPv4 would not answer.
const BACKEND = process.env.VITE_BACKEND ?? 'http://127.0.0.1:8091'

// A websocket client that gives up mid-handshake tears the TCP connection down
// while the proxy is still writing to it. Node reports ECONNABORTED /
// ECONNRESET / EPIPE, and http-proxy would otherwise print an unhandled stack
// trace for a disconnect the client caused on purpose. Swallow exactly those;
// a real backend failure (ECONNREFUSED, timeouts) is still logged.
const CLIENT_GONE = new Set(['ECONNABORTED', 'ECONNRESET', 'EPIPE', 'ERR_STREAM_WRITE_AFTER_END'])

const guardProxy = (proxy: {
  on: (event: 'error', handler: (err: NodeJS.ErrnoException, req: unknown, res: unknown) => void) => void
}) => {
  proxy.on('error', (err, _req, res) => {
    const code = err?.code ?? ''
    if (!CLIENT_GONE.has(code)) {
      console.warn(`[proxy] ${code || err?.message || 'error'} -> ${BACKEND}`)
    }
    // An HTTP response can still be answered; a socket being upgraded cannot.
    const answer = res as {
      writeHead?: (status: number, headers: Record<string, string>) => void
      headersSent?: boolean
      writableEnded?: boolean
      end?: (body?: string) => void
    }
    if (typeof answer?.writeHead === 'function' && !answer.headersSent && !answer.writableEnded) {
      answer.writeHead(502, { 'content-type': 'application/json' })
      answer.end?.(JSON.stringify({ error: 'backend_unavailable', message: `Backend ${BACKEND} is not reachable` }))
    }
  })
}

const apiProxy: ProxyOptions = {
  target: BACKEND,
  changeOrigin: true,
  ws: true,
  configure: guardProxy as ProxyOptions['configure'],
}

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': apiProxy,
      '/health': {
        target: BACKEND,
        changeOrigin: true,
        configure: guardProxy as ProxyOptions['configure'],
      },
    },
  },
})
