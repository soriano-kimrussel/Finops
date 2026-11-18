// Amounts arrive from the API as decimal strings. Intl.NumberFormat formats string input
// exactly, so no value is ever converted to a binary float on the way to the screen.

const formatters = new Map<string, Intl.NumberFormat>()

function formatter(currency: string, compact: boolean): Intl.NumberFormat {
  const key = `${currency}:${compact}`
  let f = formatters.get(key)
  if (!f) {
    f = new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency,
      ...(compact ? { notation: 'compact', maximumFractionDigits: 1 } : {}),
    })
    formatters.set(key, f)
  }
  return f
}

export function formatMoney(amount: string | null | undefined, currency: string, opts: { compact?: boolean } = {}): string {
  if (amount === null || amount === undefined || amount === '') return '—'
  return formatter(currency, opts.compact ?? false).format(amount as Intl.StringNumericLiteral)
}

export function isNegative(amount: string): boolean {
  return amount.trim().startsWith('-') && !/^-0*(\.0*)?$/.test(amount.trim())
}

export function isZero(amount: string): boolean {
  return /^-?0*(\.0*)?$/.test(amount.trim())
}

const dateFmt = new Intl.DateTimeFormat('en-US', { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' })
const dateTimeFmt = new Intl.DateTimeFormat('en-US', { dateStyle: 'medium', timeStyle: 'short' })

// Calendar dates ("2026-03-31") are not instants; format them in UTC so they never shift a day.
export function formatDate(d: string | null | undefined): string {
  if (!d) return '—'
  const parsed = new Date(`${d.slice(0, 10)}T00:00:00Z`)
  return Number.isNaN(parsed.getTime()) ? d : dateFmt.format(parsed)
}

export function formatDateTime(ts: string | null | undefined): string {
  if (!ts) return '—'
  const parsed = new Date(ts)
  return Number.isNaN(parsed.getTime()) ? ts : dateTimeFmt.format(parsed)
}

export function formatPercent(value: string | null | undefined): string {
  if (value === null || value === undefined) return '—'
  return `${value}%`
}

export function todayISO(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export function addDaysISO(iso: string, days: number): string {
  const d = new Date(`${iso}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + days)
  return d.toISOString().slice(0, 10)
}

export function humanize(s: string): string {
  const t = s.replace(/_/g, ' ')
  return t.charAt(0).toUpperCase() + t.slice(1)
}
