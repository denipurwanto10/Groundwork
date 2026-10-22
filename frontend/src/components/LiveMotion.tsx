import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

const prefersReducedMotion = () =>
  typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

/**
 * A number that glides to its new value instead of being swapped. The text
 * node is the same element the whole time, so nothing flashes or re-mounts
 * when a position frame arrives.
 */
export function AnimatedNumber({ value, duration = 450 }: { value: number; duration?: number }) {
  const [shown, setShown] = useState(value)
  const from = useRef(value)
  const raf = useRef(0)

  useEffect(() => {
    if (prefersReducedMotion() || from.current === value) {
      from.current = value
      setShown(value)
      return
    }
    const start = performance.now()
    const a = from.current
    const tick = (t: number) => {
      const p = Math.min(1, (t - start) / duration)
      const eased = 1 - Math.pow(1 - p, 3)
      const v = a + (value - a) * eased
      from.current = v
      setShown(v)
      if (p < 1) raf.current = requestAnimationFrame(tick)
    }
    raf.current = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf.current)
  }, [value, duration])

  return <>{Math.round(shown)}</>
}

/**
 * FLIP reorder: when the children of `listRef` change order (a unit goes from
 * moving to idle, say), each row slides from its old spot to the new one
 * rather than being torn out and drawn again. Rows are matched by
 * `data-flip-id`.
 */
export function useFlipList(listRef: RefObject<HTMLElement | null>, orderKey: string) {
  const prev = useRef(new Map<string, number>())

  useLayoutEffect(() => {
    const list = listRef.current
    if (!list) return
    const rows = Array.from(list.querySelectorAll<HTMLElement>(':scope > [data-flip-id]'))
    const next = new Map<string, number>()
    const reduce = prefersReducedMotion()

    for (const el of rows) {
      const id = el.dataset.flipId as string
      const top = el.getBoundingClientRect().top
      next.set(id, top)
      const old = prev.current.get(id)
      if (reduce || old === undefined) continue
      const dy = old - top
      if (Math.abs(dy) < 1) continue
      el.animate(
        [{ transform: `translateY(${dy}px)` }, { transform: 'translateY(0)' }],
        { duration: 380, easing: 'cubic-bezier(0.22, 1, 0.36, 1)' },
      )
    }
    prev.current = next
  }, [listRef, orderKey])
}
