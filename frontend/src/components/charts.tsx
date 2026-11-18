import { Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { formatMoney } from '@/lib/format'

// Categorical slots 1 and 2 of the validated reference palette (adjacent CVD ΔE 9.1).
export const SERIES = ['#2a78d6', '#eb6834'] as const
const INK_MUTED = '#898781'
const GRID = '#e1e0d9'

export type Series = { key: string; label: string }

type Row = Record<string, string>

// Amounts are converted to numbers only to size the bars; every value shown as text
// (tooltips, axis labels) is formatted from the original decimal string.
function toPlot(rows: Row[], keys: string[]) {
  return rows.map((r) => {
    const out: Record<string, string | number> = { ...r }
    for (const k of keys) out[`__${k}`] = Number(r[k] ?? 0)
    return out
  })
}

function monthLabel(m: string) {
  const [y, mo] = m.split('-')
  if (!y || !mo) return m
  return new Date(Date.UTC(Number(y), Number(mo) - 1, 1)).toLocaleString('en-US', { month: 'short', timeZone: 'UTC' })
}

export function MoneyBarChart({
  rows,
  xKey,
  series,
  currency,
  height = 260,
  horizontal = false,
  ariaLabel,
}: {
  rows: Row[]
  xKey: string
  series: Series[]
  currency: string
  height?: number
  horizontal?: boolean
  ariaLabel: string
}) {
  const data = toPlot(rows, series.map((s) => s.key))
  const isMonth = /^\d{4}-\d{2}$/.test(rows[0]?.[xKey] ?? '')
  const fmtTick = (v: number) => formatMoney(String(v), currency, { compact: true })
  const category = (v: string) => (isMonth ? monthLabel(v) : v.length > 18 ? `${v.slice(0, 17)}…` : v)

  return (
    <div role="img" aria-label={ariaLabel} style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} layout={horizontal ? 'vertical' : 'horizontal'} margin={{ top: 8, right: 12, bottom: 0, left: horizontal ? 8 : 0 }} barGap={2} barCategoryGap="28%">
          <CartesianGrid stroke={GRID} strokeWidth={1} vertical={horizontal} horizontal={!horizontal} />
          {horizontal ? (
            <>
              <XAxis type="number" tickFormatter={fmtTick} tick={{ fill: INK_MUTED, fontSize: 12 }} axisLine={false} tickLine={false} />
              <YAxis type="category" dataKey={xKey} tickFormatter={category} width={140} tick={{ fill: INK_MUTED, fontSize: 12 }} axisLine={false} tickLine={false} />
            </>
          ) : (
            <>
              <XAxis dataKey={xKey} tickFormatter={category} tick={{ fill: INK_MUTED, fontSize: 12 }} axisLine={{ stroke: '#c3c2b7' }} tickLine={false} />
              <YAxis tickFormatter={fmtTick} tick={{ fill: INK_MUTED, fontSize: 12 }} axisLine={false} tickLine={false} width={64} />
            </>
          )}
          <Tooltip
            cursor={{ fill: 'rgba(11,11,11,0.04)' }}
            content={({ active, payload, label }) => {
              if (!active || !payload?.length) return null
              const row = payload[0]?.payload as Row
              return (
                <div className="rounded-md border border-slate-200 bg-white px-3 py-2 text-xs shadow-md">
                  <p className="mb-1 font-medium text-slate-900">{isMonth ? monthLabel(String(label)) + ' ' + String(label).slice(0, 4) : String(label)}</p>
                  {series.map((s, i) => (
                    <p key={s.key} className="flex items-center gap-2 text-slate-600">
                      <span className="inline-block size-2 rounded-sm" style={{ background: SERIES[i] }} />
                      {s.label}: <span className="font-medium tabular-nums text-slate-900">{formatMoney(row[s.key], currency)}</span>
                    </p>
                  ))}
                </div>
              )
            }}
          />
          {series.length > 1 && <Legend iconType="square" iconSize={10} wrapperStyle={{ fontSize: 12, color: '#52514e' }} />}
          {series.map((s, i) => (
            <Bar key={s.key} dataKey={`__${s.key}`} name={s.label} fill={SERIES[i]} radius={horizontal ? [0, 4, 4, 0] : [4, 4, 0, 0]} maxBarSize={28} stroke="#fff" strokeWidth={1} />
          ))}
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
