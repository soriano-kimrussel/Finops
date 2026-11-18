import { Select } from './ui'
import { DateFilter } from './data'
import { todayISO } from '@/lib/format'

function monthsBack(n: number) {
  const now = new Date()
  const d = new Date(Date.UTC(now.getFullYear(), now.getMonth() - n, 1))
  return d.toISOString().slice(0, 10)
}

export function presetRange(preset: string): { from: string; to: string } {
  const today = todayISO()
  switch (preset) {
    case 'this_month':
      return { from: monthsBack(0), to: today }
    case 'last_3':
      return { from: monthsBack(2), to: today }
    case 'ytd':
      return { from: `${today.slice(0, 4)}-01-01`, to: today }
    case 'last_12':
    default:
      return { from: monthsBack(11), to: today }
  }
}

const presets = [
  { value: 'this_month', label: 'This month' },
  { value: 'last_3', label: 'Last 3 months' },
  { value: 'ytd', label: 'Year to date' },
  { value: 'last_12', label: 'Last 12 months' },
  { value: 'custom', label: 'Custom range' },
]

export function DateRangeControl({
  preset,
  from,
  to,
  onChange,
}: {
  preset: string
  from: string
  to: string
  onChange: (v: { preset: string; from: string; to: string }) => void
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Select
        aria-label="Date range"
        className="w-44"
        value={preset || 'last_12'}
        onChange={(e) => {
          const p = e.target.value
          onChange(p === 'custom' ? { preset: p, from, to } : { preset: p, ...presetRange(p) })
        }}
      >
        {presets.map((p) => (
          <option key={p.value} value={p.value}>
            {p.label}
          </option>
        ))}
      </Select>
      {preset === 'custom' && (
        <>
          <DateFilter label="From" value={from} onChange={(v) => onChange({ preset, from: v, to })} />
          <DateFilter label="to" value={to} onChange={(v) => onChange({ preset, from, to: v })} />
        </>
      )}
    </div>
  )
}

// Resolves the effective from/to for URL filter state.
export function effectiveRange(values: { range: string; from: string; to: string }) {
  if (values.range === 'custom' && values.from && values.to) return { from: values.from, to: values.to }
  return presetRange(values.range || 'last_12')
}
