import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router'

export function useDebounced<T>(value: T, delay = 300): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), delay)
    return () => clearTimeout(id)
  }, [value, delay])
  return v
}

// URL-backed filter state, so filtered views can be shared and survive reloads.
export function useFilters<K extends string>(keys: readonly K[]) {
  const [params, setParams] = useSearchParams()
  const values = Object.fromEntries(keys.map((k) => [k, params.get(k) ?? ''])) as Record<K, string>
  const set = (k: K, v: string) => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (v) next.set(k, v)
        else next.delete(k)
        if (k !== 'offset') next.delete('offset')
        return next
      },
      { replace: true },
    )
  }
  return { values, set }
}
