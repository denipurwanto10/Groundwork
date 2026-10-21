import type { VehicleKind } from '../lib/types'
import { normalizeVehicleKind } from '../lib/types'

/**
 * Illustrated vehicles (flat, ride-hailing style) instead of line glyphs.
 * Colours come from CSS variables so the status colour tints the body:
 *   --vb (body)  --vb-light (roof / highlight)  --vb-dark (trim)
 * Two views per kind: TOP (map marker, faces north, rotated by heading) and
 * SIDE (list rows and detail card, faces right).
 */

const GLASS = '#1d2733'
const TYRE = '#161a21'

/* ---------------------------- top view, 40 x 60 ---------------------------- */
const TOP: Record<VehicleKind, string> = {
  van: `
    <rect x="7" y="14" width="3" height="10" rx="1.5" fill="${TYRE}"/><rect x="30" y="14" width="3" height="10" rx="1.5" fill="${TYRE}"/>
    <rect x="7" y="40" width="3" height="10" rx="1.5" fill="${TYRE}"/><rect x="30" y="40" width="3" height="10" rx="1.5" fill="${TYRE}"/>
    <rect x="5.8" y="20" width="4" height="3" rx="1.2" fill="var(--vb-dark)"/><rect x="30.2" y="20" width="4" height="3" rx="1.2" fill="var(--vb-dark)"/>
    <rect x="9" y="5" width="22" height="50" rx="8.5" fill="var(--vb)"/>
    <path d="M9 14c0-5 3.5-9 8.5-9H20v50h-2.5C12.5 55 9 51 9 46z" fill="#fff" opacity=".16"/>
    <path d="M11.500 17.500Q20 14.600 28.500 17.500L27.500 25Q20 23.400 12.500 25z" fill="${GLASS}"/>
    <path d="M14 18.500 17 18l-2 5.500-2 .2z" fill="#fff" opacity=".22"/>
    <rect x="11" y="27" width="18" height="22" rx="4" fill="var(--vb-light)"/>
    <path d="M11 33h18M11 39h18M11 45h18" stroke="var(--vb-dark)" stroke-opacity=".22" stroke-width=".8"/>
    <rect x="12.500" y="50.500" width="15" height="2.200" rx="1.100" fill="${GLASS}"/>
    <rect x="11" y="6.600" width="5.500" height="2" rx="1" fill="#fff7c4"/><rect x="23.500" y="6.600" width="5.500" height="2" rx="1" fill="#fff7c4"/>
    <rect x="11" y="53" width="4.500" height="1.600" rx=".8" fill="#ff4d4f"/><rect x="24.500" y="53" width="4.500" height="1.600" rx=".8" fill="#ff4d4f"/>`,
  pickup: `
    <rect x="7" y="13" width="3" height="10" rx="1.5" fill="${TYRE}"/><rect x="30" y="13" width="3" height="10" rx="1.5" fill="${TYRE}"/>
    <rect x="7" y="39" width="3" height="10" rx="1.5" fill="${TYRE}"/><rect x="30" y="39" width="3" height="10" rx="1.5" fill="${TYRE}"/>
    <rect x="5.800" y="19" width="4" height="3" rx="1.200" fill="var(--vb-dark)"/><rect x="30.200" y="19" width="4" height="3" rx="1.200" fill="var(--vb-dark)"/>
    <rect x="10" y="4" width="20" height="26" rx="8" fill="var(--vb)"/>
    <rect x="9" y="29" width="22" height="27" rx="3" fill="var(--vb-dark)"/>
    <rect x="11.500" y="31.500" width="17" height="22" rx="1.800" fill="#2a2f38"/>
    <path d="M11.500 37.500h17M11.500 43h17M11.500 48.500h17" stroke="#fff" stroke-opacity=".13" stroke-width="1"/>
    <path d="M10 13c0-5 3-9 8-9h2v26h-2c-5 0-8-4-8-9z" fill="#fff" opacity=".16"/>
    <path d="M12 12.500Q20 10.300 28 12.500L27 18.500Q20 17.200 13 18.500z" fill="${GLASS}"/>
    <rect x="12" y="19.500" width="16" height="6.500" rx="2.500" fill="var(--vb-light)"/>
    <rect x="13" y="27" width="14" height="1.800" rx=".9" fill="${GLASS}"/>
    <rect x="11.500" y="5.600" width="5" height="2" rx="1" fill="#fff7c4"/><rect x="23.500" y="5.600" width="5" height="2" rx="1" fill="#fff7c4"/>
    <rect x="10.500" y="54" width="4.500" height="1.600" rx=".8" fill="#ff4d4f"/><rect x="25" y="54" width="4.500" height="1.600" rx=".8" fill="#ff4d4f"/>`,
  truck: `
    <rect x="8" y="7" width="3" height="8" rx="1.500" fill="${TYRE}"/><rect x="29" y="7" width="3" height="8" rx="1.500" fill="${TYRE}"/>
    <rect x="6" y="40" width="3.500" height="12" rx="1.700" fill="${TYRE}"/><rect x="30.500" y="40" width="3.500" height="12" rx="1.700" fill="${TYRE}"/>
    <rect x="5.500" y="9" width="4" height="3" rx="1.200" fill="var(--vb-dark)"/><rect x="30.500" y="9" width="4" height="3" rx="1.200" fill="var(--vb-dark)"/>
    <rect x="10" y="3" width="20" height="17" rx="6" fill="var(--vb)"/>
    <path d="M11.500 8.500Q20 6.600 28.500 8.500L27.500 14Q20 12.800 12.500 14z" fill="${GLASS}"/>
    <rect x="12" y="15" width="16" height="3.500" rx="1.500" fill="var(--vb-light)"/>
    <rect x="8" y="21" width="24" height="37" rx="3" fill="#eef1f6"/>
    <rect x="8" y="21" width="24" height="37" rx="3" fill="none" stroke="#000" stroke-opacity=".12"/>
    <rect x="8" y="33" width="24" height="9" fill="var(--vb)"/>
    <path d="M12 24h16M12 28h16M12 46h16M12 50h16M12 54h16" stroke="#000" stroke-opacity=".1" stroke-width=".9"/>
    <path d="M10 8c0-3 2.500-5 5-5h5v17h-5c-2.500 0-5-2-5-5z" fill="#fff" opacity=".14"/>
    <rect x="11.500" y="3.800" width="5" height="1.800" rx=".9" fill="#fff7c4"/><rect x="23.500" y="3.800" width="5" height="1.800" rx=".9" fill="#fff7c4"/>
    <rect x="10" y="57" width="5" height="1.500" rx=".7" fill="#ff4d4f"/><rect x="25" y="57" width="5" height="1.500" rx=".7" fill="#ff4d4f"/>`,
  motorcycle: `
    <rect x="18" y="4" width="4" height="13" rx="2" fill="${TYRE}"/>
    <rect x="17.700" y="40" width="4.600" height="16" rx="2.300" fill="${TYRE}"/>
    <rect x="12" y="38" width="16" height="15" rx="3" fill="#f4f6fa"/>
    <rect x="12" y="38" width="16" height="15" rx="3" fill="none" stroke="#000" stroke-opacity=".15"/>
    <rect x="12" y="43" width="16" height="4.500" fill="var(--vb)"/>
    <path d="M9 17.500h22" stroke="${TYRE}" stroke-width="2.400" stroke-linecap="round"/>
    <rect x="6.500" y="16.300" width="4" height="2.400" rx="1.200" fill="var(--vb-dark)"/><rect x="29.500" y="16.300" width="4" height="2.400" rx="1.200" fill="var(--vb-dark)"/>
    <path d="M20 8c3.200 0 5 2.600 5 6l-.8 5h-8.400l-.8-5c0-3.400 1.800-6 5-6z" fill="var(--vb-dark)"/>
    <rect x="18" y="8.800" width="4" height="2.200" rx="1.100" fill="#fff7c4"/>
    <ellipse cx="20" cy="32" rx="6.500" ry="10" fill="var(--vb)"/>
    <path d="M12.500 28c-.5 2.500 1 4 3.300 5l2.200-2.300-.6-4.700z" fill="var(--vb)"/>
    <path d="M27.500 28c.5 2.500-1 4-3.300 5l-2.200-2.300.6-4.700z" fill="var(--vb)"/>
    <path d="M13.500 26 11 19.500M26.500 26 29 19.500" stroke="var(--vb)" stroke-width="3.600" stroke-linecap="round"/>
    <ellipse cx="20" cy="30" rx="9.500" ry="4.600" fill="var(--vb)"/>
    <ellipse cx="20" cy="30" rx="9.500" ry="4.600" fill="#000" opacity=".12"/>
    <circle cx="20" cy="27" r="5.300" fill="var(--vb-light)"/>
    <path d="M15.200 25.200Q20 22.600 24.800 25.200" stroke="${GLASS}" stroke-width="2.600" stroke-linecap="round" fill="none"/>
    <circle cx="18.300" cy="26.200" r="1.200" fill="#fff" opacity=".45"/>`,
}

/* --------------------------- side view, 64 x 40 --------------------------- */
const wheel = (cx: number) =>
  `<circle cx="${cx}" cy="31" r="6" fill="${TYRE}"/><circle cx="${cx}" cy="31" r="2.600" fill="#cdd4df"/><circle cx="${cx}" cy="31" r="1" fill="#6b7686"/>`

const SIDE: Record<VehicleKind, string> = {
  van: `
    <ellipse cx="32" cy="37.500" rx="27" ry="2" fill="#000" opacity=".14"/>
    <path d="M4 31V15a4 4 0 0 1 4-4h31q4 0 6.500 3.200L55 24q3 1.500 3 5v2z" fill="var(--vb)"/>
    <path d="M4 15a4 4 0 0 1 4-4h31q4 0 6.500 3.200L47 16H4z" fill="#fff" opacity=".2"/>
    <path d="M42 14.500 51 24H41z" fill="#cfe6ff"/><path d="M42 14.500 51 24h-2z" fill="#fff" opacity=".5"/>
    <rect x="28" y="14.500" width="10" height="9.500" rx="1.500" fill="#cfe6ff"/>
    <rect x="8" y="14.500" width="16" height="9.500" rx="1.500" fill="var(--vb-light)" opacity=".85"/>
    <path d="M8 27h45" stroke="var(--vb-dark)" stroke-width="1.600" opacity=".55"/>
    <rect x="55" y="26" width="3.500" height="3" rx="1.200" fill="#fff7c4"/><rect x="3.500" y="25" width="2.200" height="3" rx="1" fill="#ff4d4f"/>
    ${wheel(15)}${wheel(46)}`,
  pickup: `
    <ellipse cx="32" cy="37.500" rx="28" ry="2" fill="#000" opacity=".14"/>
    <path d="M3 31V19h25v-6a3 3 0 0 1 3-3h10.500q3 0 5 2.800L52 20h6q3 0 3.500 3.500V31z" fill="var(--vb)"/>
    <path d="M3 19h25v3H3z" fill="var(--vb-dark)"/>
    <path d="M32 13.500h9.500l5 6.500H32z" fill="#cfe6ff"/><path d="M32 13.500h9.500l1.500 2H32z" fill="#fff" opacity=".55"/>
    <path d="M6 24h22" stroke="#fff" stroke-opacity=".25" stroke-width="1.200"/>
    <rect x="59" y="24" width="3" height="3" rx="1.200" fill="#fff7c4"/><rect x="2" y="21.500" width="2.200" height="3" rx="1" fill="#ff4d4f"/>
    ${wheel(15)}${wheel(48)}`,
  truck: `
    <ellipse cx="32" cy="37.500" rx="29" ry="2" fill="#000" opacity=".14"/>
    <rect x="2" y="5" width="37" height="26" rx="2.500" fill="#f2f5fa"/>
    <rect x="2" y="5" width="37" height="26" rx="2.500" fill="none" stroke="#000" stroke-opacity=".12"/>
    <rect x="2" y="15" width="37" height="7" fill="var(--vb)"/>
    <path d="M7 8h27M7 26h27" stroke="#000" stroke-opacity=".1"/>
    <path d="M41 12h10q3 0 5 3.500L61 22q1.500 2 1.500 5v4H41z" fill="var(--vb)"/>
    <path d="M44 14.500h6.500l4.500 7H44z" fill="#cfe6ff"/><path d="M44 14.500h6.500l1 1.500H44z" fill="#fff" opacity=".55"/>
    <rect x="60" y="24" width="3" height="3" rx="1.200" fill="#fff7c4"/>
    ${wheel(12)}${wheel(27)}${wheel(52)}`,
  motorcycle: `
    <ellipse cx="32" cy="37.500" rx="27" ry="2" fill="#000" opacity=".14"/>
    <rect x="3" y="11" width="15" height="13" rx="2.500" fill="#f4f6fa"/>
    <rect x="3" y="11" width="15" height="13" rx="2.500" fill="none" stroke="#000" stroke-opacity=".15"/>
    <rect x="3" y="16" width="15" height="4.500" fill="var(--vb)"/>
    <circle cx="14" cy="30" r="7" fill="${TYRE}"/><circle cx="14" cy="30" r="3" fill="#cdd4df"/><circle cx="14" cy="30" r="1.100" fill="#6b7686"/>
    <circle cx="50" cy="30" r="7" fill="${TYRE}"/><circle cx="50" cy="30" r="3" fill="#cdd4df"/><circle cx="50" cy="30" r="1.100" fill="#6b7686"/>
    <path d="M14 30 24 24h13l9 3 4 3" stroke="var(--vb-dark)" stroke-width="2.200" stroke-linecap="round" stroke-linejoin="round" fill="none"/>
    <path d="M19 25q1-6 10-6l9 1.500 7 5.500-6 3H23z" fill="var(--vb)"/>
    <path d="M45 21.500 50 30" stroke="${TYRE}" stroke-width="2.200" stroke-linecap="round"/>
    <path d="M44 19h5" stroke="${TYRE}" stroke-width="2.400" stroke-linecap="round"/>
    <rect x="46" y="22" width="5" height="3" rx="1.500" fill="#fff7c4"/>
    <path d="M30 11 35.500 21" stroke="var(--vb)" stroke-width="7.500" stroke-linecap="round"/>
    <path d="M35 14.500 43 19.500" stroke="var(--vb)" stroke-width="3.400" stroke-linecap="round"/>
    <path d="M33 21 38.500 26 36 31" stroke="var(--vb-dark)" stroke-width="3.400" stroke-linecap="round" stroke-linejoin="round" fill="none"/>
    <circle cx="33.500" cy="6.500" r="5.200" fill="var(--vb-light)"/>
    <path d="M34.500 4.800h4.600q.2 3-1.800 4.200h-2.800z" fill="${GLASS}"/>`,
}

export function vehicleTopSvg(kind: string): string {
  return `<svg viewBox="0 0 40 60" aria-hidden="true">${TOP[normalizeVehicleKind(kind)]}</svg>`
}

export function vehicleSideSvg(kind: string): string {
  return `<svg viewBox="0 0 64 40" aria-hidden="true">${SIDE[normalizeVehicleKind(kind)]}</svg>`
}

/** Side-view illustration for lists and cards. */
export function VehicleSide({ kind }: { kind: string }) {
  return <span className="va-side" aria-hidden="true" dangerouslySetInnerHTML={{ __html: vehicleSideSvg(kind) }} />
}
