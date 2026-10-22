/**
 * The map tiles.
 *
 * One style only: the Google Maps road map, served straight from Google's
 * tile servers without an API key, so the map looks exactly like the one
 * everybody uses without a billing account.
 *
 * `lyrs=m` is the road map, `lyrs=y` the satellite with labels and `lyrs=p`
 * the terrain view; swap the value for one of those to change the style.
 *
 * VITE_MAP_TILES_URL still wins: with it set, the app uses that tile source
 * (for example the official Maps Static API with a key).
 */

export const TILE_URL =
  (import.meta.env.VITE_MAP_TILES_URL as string | undefined) ||
  'https://{s}.google.com/vt/lyrs=m&hl=en&x={x}&y={y}&z={z}'

/** The four tile servers Google serves the map from. */
export const TILE_SUBDOMAINS = ['mt0', 'mt1', 'mt2', 'mt3']

/** The tile source an operator pinned in the environment, if any. */
export const CUSTOM_TILE_URL = (import.meta.env.VITE_MAP_TILES_URL as string | undefined) || ''

/** The credit belongs to the source of the tiles. */
export const MAP_ATTRIBUTION = CUSTOM_TILE_URL
  ? '&copy; <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noreferrer">OpenStreetMap</a> contributors'
  : 'Map data &copy; <a href="https://www.google.com/maps" target="_blank" rel="noreferrer">Google</a>'
