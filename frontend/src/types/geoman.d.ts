// Leaflet-Geoman ships its own typings, but the package.json has no "types"
// entry, so TypeScript never loads them on its own. Without this reference
// `map.pm`, `path.pm` and the `PM` namespace are untyped, and the zone editor
// would fall back to `any` casts everywhere.
//
// The referenced file augments the 'leaflet' module (Map.pm, Path.pm, the PM
// namespace with CreateEventHandler / UpdateEventHandler / PMMap / ...).
/// <reference path="../../node_modules/@geoman-io/leaflet-geoman-free/dist/leaflet-geoman.d.ts" />
