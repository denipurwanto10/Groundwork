# PWA — offline-first for the driver

The app is a Progressive Web App: it installs on the phone, it opens without a
network, and the driver keeps the map and the trip in a dead spot.

Three pieces do the work:

| Piece | Where | What it does |
|---|---|---|
| The manifest | `vite.config.ts` → `manifest` | The name, the icons and the colours of the installed app. Written to `dist/manifest.webmanifest`. |
| The service worker | `vite-plugin-pwa` (Workbox) | Precaches the shell of the app and keeps the map tiles. |
| The trip on the device | `src/lib/offline.ts` | The route of the driver is copied into `localStorage`. |

## The service worker

`vite-plugin-pwa` generates `dist/sw.js` at build time. There is exactly **one**
worker: the notification code of the app (`public/sw-notify.js`) is imported
into it (`workbox.importScripts`), so caching and notifications never fight
over the page.

**The shell** — `globPatterns` puts every built file (the JS, the CSS, the
icons, `index.html`) into the precache. `navigateFallback: 'index.html'` means
a navigation to any path (`/driver` included) is answered with the cached
shell, so the app opens with no network at all.

**The map tiles** — the tiles are the images Leaflet downloads for the map.
They are cached at runtime, `CacheFirst`:

```
cacheName: 'groundwork-map-tiles'
maxEntries: 800          # the cache cannot fill the phone
maxAgeSeconds: 30 days
```

The tile servers that are cached are the Google road map (`mt0`–`mt3`), the
OpenStreetMap tiles, and the host of `VITE_MAP_TILES_URL` when one is pinned
in the environment. A tile that was on screen once stays on the screen when
the signal is gone.

**The API is not cached.** The answers carry the data of one account, so they
stay out of the shared cache of the worker. The offline copy of the trip is
the one of the driver screen (below).

## The trip on the device

`src/lib/offline.ts` keeps the driver screen working without a network:

- `useOnline()` follows `navigator.onLine` and the `online` / `offline` events.
- `useDriverSnapshot()` copies the unit, the route and the trips of today into
  `localStorage` under `groundwork:driver:snapshot:<driver id>`. The write is
  throttled (the position of the unit changes several times a second), and the
  last change is written when the screen goes away. The key carries the id of
  the driver: a phone is sometimes shared, and the trip of one driver must not
  show up under the name of the next one.
- The screen starts from that copy: a driver who opens the app in a dead spot
  sees the trip at once, not a spinner. The answer of the server takes over as
  soon as it arrives.
- While there is no connection the screen says so — the banner over the map
  reads *"Anda sedang Offline. Rute disimpan di memori."* — and the button
  that starts a trip is disabled, because the trip needs the server.

## How to test it

The service worker only exists in a production build, so test it against the
built app, not against the dev server.

```bash
cd frontend
npm install
npm run build
npm run preview        # http://localhost:4173 (the /api proxy is on too)
```

Sign in with an account of the `driver` role (the screen is `/driver`) and
open the map once, so the tiles of that area are in the cache.

### 1. The worker is there

Open **DevTools → Application → Service Workers**: `sw.js` is listed and
`activated`. Under **Cache Storage** there are two caches:

- `workbox-precache-v2-http://localhost:4173/` — the shell of the app
- `groundwork-map-tiles` — the tiles of the map

**Manifest** in the same panel shows the name, the icons and the colours.
**Lighthouse → PWA** scores the installability (a manifest, a worker and a
maskable icon are all there).

### 2. The map survives the outage

**DevTools → Network → throttling → Offline**, or tick **Offline** in the
Network panel. Then reload the page:

- the app opens (the shell comes from the precache),
- the map is still drawn (the tiles come from `groundwork-map-tiles`),
- the banner *"Anda sedang Offline. Rute disimpan di memori."* is over the map,
- the trip card still shows the route, the distance and the ETA — they come
  from the copy on the device.

Undo it with **Network → throttling → No throttling**. The screen asks the
server for the fleet again and the live feed reconnects by itself.

### 3. The trip is on the device

**DevTools → Application → Local Storage → http://localhost:4173** shows
`groundwork:driver:snapshot:<driver id>`: the unit, the route and the trips of
today, as JSON. Delete the key and reload: the screen starts empty again and
fills itself from the server.

### 4. It installs

Chrome: the install icon in the address bar (or **⋮ → Install Groundwork**).
On Android the app lands on the home screen with its own icon; on iOS
**Share → Add to Home Screen**. It opens in standalone mode (no browser
chrome) and starts on `/`.

### 5. The notifications still work

The bell in the top bar asks for the permission; the crossing of a zone is
shown as a system notification by the same worker (see the README of the
frontend). The `push` handler is still there, ready for Web Push.

## Notes and limits

- **The tiles are the ones that were seen.** A driver who drives into a part
  of the map that was never on screen sees an empty background there. The
  cache holds 800 tiles, which is roughly the area a driver covers in a
  normal shift.
- **The start of a trip needs the server.** It is the one thing the driver
  screen cannot do alone; the button says so while offline.
- **The dev server has no worker.** `npm run dev` behaves exactly as before.
  Set `VITE_PWA_DEV=1` to try the worker in dev as well (it then runs from
  `/dev-sw.js`).
- **To get rid of it** (a stale worker, a new cache): DevTools → Application →
  Service Workers → **Unregister**, and clear the two caches; or in the
  browser settings, clear the site data.
