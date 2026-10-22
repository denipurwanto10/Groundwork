import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv, type ProxyOptions } from 'vite'
import { VitePWA } from 'vite-plugin-pwa'

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

/**
 * The tile servers the service worker keeps.
 *
 * The map is drawn from raster tiles. A driver who drives into a dead spot
 * still has to see the road, so every tile that was on screen once is stored
 * and handed out from the cache when the network is gone.
 *
 * Two hosts are always cached: the Google road map the app draws by default
 * and OpenStreetMap, the usual tile server of a self-hosted deployment. A
 * tile source pinned in the environment (`VITE_MAP_TILES_URL`) is added on
 * top, so every deployment gets its own tiles offline.
 */
function tilePatterns(customTileUrl: string): RegExp[] {
  const patterns: RegExp[] = [
    // The Google road map (mt0-mt3.google.com).
    /^https:\/\/mt[0-9]\.google\.com\/vt\/lyrs=/i,
    // OpenStreetMap.
    /^https:\/\/[a-z]\d?\.tile\.openstreetmap\.org\//i,
  ]

  const custom = tileHostPattern(customTileUrl)
  if (custom) patterns.push(custom)
  return patterns
}

/**
 * The host of a tile template, turned into a pattern.
 *
 * The template carries placeholders ({s}, {x}, {y}, {z}); a placeholder in the
 * host matches any host part, so `{s}.tiles.example.com` covers every
 * subdomain of that server.
 */
function tileHostPattern(template: string): RegExp | null {
  const match = /^(?:https?:)?\/\/([^/?#]+)/i.exec(template.trim())
  if (!match) return null
  const pattern = match[1]
    .split(/(\{[^}]*\})/)
    .map((part) =>
      part.startsWith('{')
        ? '[^.]+'
        : part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'),
    )
    .join('')
  return new RegExp(`^https?://${pattern}/`, 'i')
}

export default defineConfig(({ mode }) => {
  // The tile source can be pinned in .env, so the workbox rules are built
  // from the same file the app reads.
  const env = loadEnv(mode, process.cwd(), '')

  return {
    plugins: [
      react(),
      VitePWA({
        // The worker takes over as soon as a new build is on the server: the
        // driver must not be stuck on an old shell after a deploy.
        registerType: 'autoUpdate',
        // The files of `public/` (the icons, the favicon) are already in the
        // precache: the glob below covers them, and they are added to the
        // precache a second time otherwise.
        includeManifestIcons: false,
        manifest: {
          id: '/',
          name: 'Groundwork — Field operations, mapped',
          short_name: 'Groundwork',
          description:
            'Live fleet tracking and delivery routes for field operations. Works offline.',
          lang: 'en',
          start_url: '/',
          scope: '/',
          // The brand red of the app.
          theme_color: '#d0171e',
          background_color: '#06080d',
          display: 'standalone',
          orientation: 'portrait',
          categories: ['business', 'productivity', 'navigation'],
          icons: [
            { src: '/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
            { src: '/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
            { src: '/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
          ],
          shortcuts: [
            {
              name: 'Driver view',
              short_name: 'Driver',
              description: 'Your unit and your trip',
              url: '/driver',
              icons: [{ src: '/icon-192.png', sizes: '192x192', type: 'image/png' }],
            },
          ],
        },
        workbox: {
          // The shell of the app: with these files in the cache the app
          // opens with no network at all, on any screen.
          globPatterns: ['**/*.{js,css,html,svg,png,ico,woff2}'],
          // /driver is a path, not a file: every navigation falls back to the
          // shell, so the driver screen opens offline too.
          navigateFallback: 'index.html',
          cleanupOutdatedCaches: true,
          skipWaiting: true,
          clientsClaim: true,
          // The notification code of the app lives in its own file and is
          // imported into the generated worker, so there is exactly one
          // service worker: one that caches and one that notifies.
          importScripts: ['/sw-notify.js'],
          runtimeCaching: [
            ...tilePatterns(env.VITE_MAP_TILES_URL ?? '').map((urlPattern) => ({
              urlPattern,
              handler: 'CacheFirst' as const,
              options: {
                cacheName: 'groundwork-map-tiles',
                expiration: {
                  // A tile is small, but there are many of them: the cache is
                  // capped so it cannot fill the phone.
                  maxEntries: 800,
                  maxAgeSeconds: 60 * 60 * 24 * 30,
                  purgeOnQuotaError: true,
                },
                cacheableResponse: { statuses: [0, 200] },
              },
            })),
            // The API itself is never cached here: the answers carry the data
            // of one account, and the offline copy of the trip is kept by the
            // driver screen in localStorage instead.
          ],
        },
        // The service worker only exists in a production build. The dev
        // server keeps working without it, exactly as before; set
        // VITE_PWA_DEV=1 to try the worker in dev as well.
        devOptions: { enabled: process.env.VITE_PWA_DEV === '1' },
      }),
    ],
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
    // `npm run preview` serves the production build, the one that carries
    // the service worker. The proxy is repeated here, so the app talks to
    // the backend while it is being tested as an installed app.
    preview: {
      port: 4173,
      proxy: {
        '/api': apiProxy,
        '/health': {
          target: BACKEND,
          changeOrigin: true,
          configure: guardProxy as ProxyOptions['configure'],
        },
      },
    },
  }
})
