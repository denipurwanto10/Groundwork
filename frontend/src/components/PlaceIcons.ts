/**
 * Category glyphs for place markers (24x24, stroke based, white on the badge).
 * Keys are the canonical entity types; unknown types fall back to a map pin.
 */
const G = {
  pin: '<path d="M12 21s-6.5-5.6-6.5-11a6.5 6.5 0 0 1 13 0c0 5.400-6.500 11-6.500 11z"/><circle cx="12" cy="10" r="2.400"/>',
  flag: '<path d="M6 21V4"/><path d="M6 5h11l-2.500 4 2.500 4H6"/>',
  monument: '<path d="M12 2.500 15 8l-1 13h-4L9 8z"/><path d="M7 21h10"/>',
  historic: '<path d="M3 9.500 12 4l9 5.500"/><path d="M5 10v8M9.500 10v8M14.500 10v8M19 10v8"/><path d="M3 21h18"/>',
  park: '<path d="M12 3 6.500 11h3L5.500 17h13l-4-6h3z"/><path d="M12 17v4"/>',
  recreation: '<circle cx="12" cy="12" r="9"/><path d="M8 14.500c1.200 1.800 2.400 2.500 4 2.500s2.800-.7 4-2.500"/><circle cx="9" cy="10" r=".6"/><circle cx="15" cy="10" r=".6"/>',
  zoo: '<circle cx="7" cy="9" r="1.800"/><circle cx="12" cy="6.500" r="1.800"/><circle cx="17" cy="9" r="1.800"/><path d="M12 11.500c-3 0-5.500 3.200-5.500 5.600 0 1.600 1.300 2.400 2.800 2.400 1 0 1.700-.5 2.700-.5s1.700.5 2.700.5c1.500 0 2.800-.8 2.800-2.400 0-2.400-2.500-5.600-5.500-5.600z"/>',
  culinary: '<path d="M7 3v8M5 3v5a2 2 0 0 0 4 0V3"/><path d="M7 11v10"/><path d="M17 3c-2 1.500-3 4-3 7h3v11"/>',
  lodging: '<path d="M3 18V7"/><path d="M3 14h18v4"/><path d="M21 14v-2.500A2.500 2.500 0 0 0 18.500 9H11v5"/><circle cx="7" cy="11" r="1.800"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
} as const

const BY_TYPE: Record<string, keyof typeof G> = {
  destination: 'flag',
  monument: 'monument',
  historic: 'historic',
  park: 'park',
  recreation: 'recreation',
  zoo: 'zoo',
  culinary: 'culinary',
  lodging: 'lodging',
}

export function placeGlyph(canonical: string | 'draft'): string {
  const key = canonical === 'draft' ? 'plus' : (BY_TYPE[canonical] ?? 'pin')
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${G[key]}</svg>`
}
