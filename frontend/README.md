# Entity Manager — Frontend

React + TypeScript + Vite + Leaflet. Talks to the Go backend (`GET/POST /api/entities`,
`GET/PUT/DELETE /api/entities/:id`, `GET /health`). No backend changes were made.

## Prerequisites

- Backend running on `:8080` with PostgreSQL (`entity_management`, migration 001 applied).
- Node.js + npm.

## Configure

```bash
cp .env.example .env   # optional
```

| Variable | Default | Meaning |
|---|---|---|
| `VITE_API_BASE_URL` | _(empty = same origin)_ | Backend origin. Leave empty in dev (Vite proxy forwards `/api` + `/health` to `http://localhost:8080`). Set to the backend URL for production builds. |

## Run

```bash
npm install
npm run dev      # dev server (proxy handles CORS — backend has none)
npm run build    # tsc + production build → dist/
npm run lint     # oxlint
```

Open the dev URL (default `http://localhost:5173`; if busy, pass `--port 5174`).

## Structure

```
src/
  lib/api.ts      # Fetch client: ApiError{code,status,fields}, CRUD + health
  lib/types.ts    # shared types + validation (mirrors the backend rules)
  lib/playback.ts # playback of a recorded trip: state, cursor, animation
  lib/vehicleStream.ts  # live WebSocket feed with reconnect
  lib/notifications.ts  # desktop notifications: permission, service worker, crossing notice
  lib/offline.ts  # offline-first: the online state, the copy of the trip on the device
  lib/route.ts    # the two screens, addressed by the path (/ and /driver)
  lib/mapTiles.ts # the tile source and its credit
  lib/session.ts  # token storage (access + refresh)
  components/
    EntityMap.tsx      # Leaflet map: markers from backend, click-to-pick coords
    EntityList.tsx     # list + loading/empty states
    EntityDetail.tsx   # detail + edit/delete actions
    EntityForm.tsx     # create/edit modal, client validation + server field errors
    ConfirmDialog.tsx  # delete confirmation
    VehicleLayer.tsx   # live fleet markers (heading, status colour)
    RouteLayer.tsx     # delivery routes, one colour per unit
    PlaybackLayer.tsx  # recorded track + the ghost marker that walks it
    PlaybackPanel.tsx  # day picker, slider, play/pause, speed
    NotificationBell.tsx  # the bell that asks for the notification permission
    ActivityLogPanel.tsx  # the trail of successful writes (admin only)
    DriverView.tsx     # the driver screen: full-screen map + trip card
    DriverMap.tsx      # the map of the driver screen (unit + its route only)
  App.tsx         # state, tabs, toasts, error banner
public/
  sw-notify.js   # the notification half of the service worker (imported into the generated one)
  icon-*.png     # the icons of the installed app
```

## PWA (offline-first)

The app is a Progressive Web App: it installs on the phone, it opens without a
network, and the driver keeps the map and the trip in a dead spot.

- `vite-plugin-pwa` generates the service worker and the manifest. The shell
  of the app is precached, and the **map tiles are cached at runtime**
  (`groundwork-map-tiles`), so a tile that was on screen once stays on screen.
- The notification code (`public/sw-notify.js`) is imported into that worker,
  so the app runs on **one** service worker: the one that caches and the one
  that notifies.
- `lib/offline.ts` keeps a copy of the trip (the unit, the route, the trips of
  today) in `localStorage`. While there is no connection the driver screen
  shows that copy and says so over the map.
- `npm run preview` serves the built app (with the proxy to the backend), and
  that is the build to test: the dev server has no worker.

The full walkthrough — DevTools, the two caches, the offline simulation, the
installation — is in [PWA.md](PWA.md).

## Desktop notifications (zone crossings)

A zone crossing is reported twice: as a toast inside the page and as a system
notification in the corner of the screen (Windows/macOS). The toast is gone as
soon as the tab loses focus, the notification stays.

- The **bell** in the top bar (only rendered after sign-in) asks for the
  permission with `Notification.requestPermission()`. The request only works
  from a user gesture, so the click on the bell is what unlocks it. Granted:
  the bell turns accent-coloured and clicking it again sends a test
  notification. Blocked by the browser: the bell is dimmed and disabled.
- `lib/notifications.ts` reuses the service worker of the PWA (the one that
  also caches the map tiles) and shows the notification through
  `ServiceWorkerRegistration.showNotification()` — the path that keeps
  working while the tab sits in the background. `new Notification()` is the
  fallback for browsers without a service worker.
- The notice is raised from the `geofence` frame of the live feed
  (`lib/vehicleStream.ts`). One crossing is one notification, tagged with the
  crossing id, so the same crossing never stacks up twice.
- Both the Notification API and the service worker need a **secure context**:
  `https://` or `http://localhost` / `http://127.0.0.1`. Opened through a LAN
  IP over plain http the bell is not rendered at all.
- The service worker also has a `push` handler, ready for the next stage:
  Web Push (VAPID) from the backend, which is what makes the notification
  arrive when the browser is closed. Until the backend sends push messages,
  the notification needs the tab to stay open.

### Testing it

1. Sign in, click the bell in the top bar and allow the notification. A test
   notification shows up in the corner of the screen.
2. Move a unit across a zone boundary. The "Peringatan Zona" notification
   appears (and the toast in the page). Without the simulator, two position
   reports are enough — one inside the zone, one outside:

   ```bash
   curl -X POST http://127.0.0.1:8091/api/vehicles/1/position \
     -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
     -d '{"lat": -6.200, "lng": 107.500, "speed_kmh": 30, "status": "moving"}'
   ```

3. Minimize the browser window and repeat step 2: the notification still
   shows up.

## Activity log (admin only)

The **Activity Log** tab in the sidebar (visible to admins only) lists what
everybody changed: when it happened, who did it, and what they did.

- The trail is written by the backend on every successful write, so the
  panel only has to read it: `GET /api/audit-logs` with
  `?limit=&offset=&action=&entity_type=&q=`.
- The tab loads the trail when it is opened, and the toolbar has a search
  box (the text is looked for in the message and in the name of the
  account), a filter for the kind of action, and a reload button.
- The panel is a list of rows, not a table: it lives in the narrow side
  column, where four columns do not fit.

## Driver view (role `driver`)

A driver never sees the dashboard. Signing in with the `driver` role lands
on `/driver`: a screen made for a phone in one hand.

- **Two screens, addressed by the path.** There is no router library:
  `lib/route.ts` reads `location.pathname` and pushes a new one. The sign-in
  sends the driver to `/driver` and everybody else to `/`; opening `/driver`
  with another role sends the visitor back to the map.
- **The map** (`DriverMap.tsx`) is a map of its own — the dashboard one
  carries too many layers for a small screen. It draws the unit and its
  route and nothing else. The camera fits the whole line when a trip
  appears and then only nudges the unit back into view.
- **The card at the bottom** holds the unit, the trip (destination, distance
  left, ETA, progress) and the trips of today. It can be collapsed to one
  line so the map gets the whole screen.
- **"Start trip"** calls `POST /api/vehicles/:id/route/start`. The button is
  62 px tall and sticks to the bottom of the card, so it stays reachable
  with a thumb while the card scrolls.

## Behavior notes

- Every mutation re-fetches the list from the backend; map markers and list stay in sync.
- Form validates locally first (same rules as backend), then surfaces backend
  `validation_failed` field errors per input.
- Delete requires confirmation. Toasts report success/failure; a retry banner
  appears when the list fails to load.
- Mobile: panels stack (`max-width: 860px`), map shrinks to 360px.
- Playback: the ghost marker is moved on the Leaflet object itself, so the
  track runs at the frame rate of the screen while the app is re-rendered only
  a few times a second (for the slider and the clock).
