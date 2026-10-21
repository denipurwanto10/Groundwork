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
  App.tsx         # state, tabs, toasts, error banner
```

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
