import clsx from 'clsx'
import { ChevronLeft, ChevronRight, Search } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useDebounced } from '@/lib/hooks'
import { formatMoney, humanize } from '@/lib/format'
import type { BudgetLevel, BudgetSummary, DisplayStatus, ExpenseStatus, ProjectStatus } from '@/lib/models'
import { Badge, Button, Input, Select, type BadgeTone } from './ui'

export function SearchBox({ value, onChange, placeholder = 'Search…', label = 'Search' }: { value: string; onChange: (v: string) => void; placeholder?: string; label?: string }) {
  const [text, setText] = useState(value)
  const debounced = useDebounced(text)
  useEffect(() => {
    if (debounced !== value) onChange(debounced)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debounced])
  return (
    <div className="relative w-full sm:w-72">
      <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-slate-400" aria-hidden />
      <Input aria-label={label} type="search" className="pl-9" placeholder={placeholder} value={text} onChange={(e) => setText(e.target.value)} />
    </div>
  )
}

export function FilterSelect({ label, value, onChange, options }: { label: string; value: string; onChange: (v: string) => void; options: { value: string; label: string }[] }) {
  return (
    <Select aria-label={label} className="w-full sm:w-44" value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{label}: All</option>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </Select>
  )
}

export function DateFilter({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <label className="flex items-center gap-2 text-sm text-slate-500">
      <span className="whitespace-nowrap">{label}</span>
      <Input type="date" className="w-40" value={value} onChange={(e) => onChange(e.target.value)} />
    </label>
  )
}

export function FilterBar({ children }: { children: ReactNode }) {
  return <div className="flex flex-col gap-2 border-b border-slate-100 p-4 sm:flex-row sm:flex-wrap sm:items-center">{children}</div>
}

export function Pagination({ total, limit, offset, onChange }: { total: number; limit: number; offset: number; onChange: (offset: number) => void }) {
  if (total <= limit && offset === 0) return null
  const from = total === 0 ? 0 : offset + 1
  const to = Math.min(offset + limit, total)
  return (
    <div className="flex items-center justify-between border-t border-slate-100 px-4 py-3 text-sm text-slate-500">
      <span>
        {from}–{to} of {total}
      </span>
      <div className="flex gap-2">
        <Button variant="secondary" size="sm" disabled={offset === 0} onClick={() => onChange(Math.max(0, offset - limit))} aria-label="Previous page">
          <ChevronLeft className="size-4" />
        </Button>
        <Button variant="secondary" size="sm" disabled={to >= total} onClick={() => onChange(offset + limit)} aria-label="Next page">
          <ChevronRight className="size-4" />
        </Button>
      </div>
    </div>
  )
}

const invoiceTones: Record<DisplayStatus, BadgeTone> = {
  draft: 'gray',
  sent: 'blue',
  partially_paid: 'amber',
  paid: 'green',
  overdue: 'red',
  void: 'gray',
}

export function InvoiceStatusBadge({ status }: { status: DisplayStatus }) {
  return <Badge tone={invoiceTones[status]}>{status === 'void' ? 'Void' : humanize(status)}</Badge>
}

const projectTones: Record<ProjectStatus, BadgeTone> = { planned: 'purple', active: 'blue', on_hold: 'amber', completed: 'green', cancelled: 'gray' }

export function ProjectStatusBadge({ status }: { status: ProjectStatus }) {
  return <Badge tone={projectTones[status]}>{humanize(status)}</Badge>
}

const expenseTones: Record<ExpenseStatus, BadgeTone> = { pending: 'amber', approved: 'green', rejected: 'red' }

export function ExpenseStatusBadge({ status }: { status: ExpenseStatus }) {
  return <Badge tone={expenseTones[status]}>{humanize(status)}</Badge>
}

const levelColor: Record<BudgetLevel, string> = { none: 'bg-slate-300', ok: 'bg-emerald-500', warning: 'bg-amber-500', over: 'bg-red-500' }

export function BudgetMeter({ summary, currency, compact }: { summary: BudgetSummary; currency: string; compact?: boolean }) {
  if (summary.level === 'none' && summary.actual === summary.total) {
    return <span className="text-sm text-slate-400">No budget</span>
  }
  const pct = Math.min(100, Number.parseFloat(summary.percent_used) || (summary.level === 'over' ? 100 : 0))
  return (
    <div className={clsx('min-w-40', compact ? 'space-y-1' : 'space-y-1.5')}>
      <div className="flex items-baseline justify-between gap-3 text-xs">
        <span className="font-medium text-slate-700 tabular-nums">
          {formatMoney(summary.actual, currency)} <span className="text-slate-400">of {formatMoney(summary.total, currency)}</span>
        </span>
        <span
          className={clsx(
            'font-semibold tabular-nums',
            summary.level === 'over' && 'text-red-600',
            summary.level === 'warning' && 'text-amber-600',
            summary.level === 'ok' && 'text-slate-600',
          )}
        >
          {summary.level === 'none' ? '—' : `${summary.percent_used}%`}
        </span>
      </div>
      <div
        className="h-1.5 w-full overflow-hidden rounded-full bg-slate-100"
        role="meter"
        aria-label="Budget used"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
      >
        <div className={clsx('h-full rounded-full', levelColor[summary.level])} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

export function BudgetWarning({ summary }: { summary: BudgetSummary }) {
  if (summary.level === 'warning') return <Badge tone="amber">Approaching budget</Badge>
  if (summary.level === 'over') return <Badge tone="red">Over budget</Badge>
  return null
}

export function Money({ amount, currency, className }: { amount: string | null | undefined; currency: string; className?: string }) {
  return <span className={clsx('tabular-nums', className)}>{formatMoney(amount, currency)}</span>
}

export function SourceBadge({ source }: { source?: string }) {
  if (source !== 'quickbooks') return null
  return <Badge tone="purple">QuickBooks</Badge>
}

export function DetailList({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2">
      {items.map((i) => (
        <div key={i.label}>
          <dt className="text-xs font-medium uppercase tracking-wide text-slate-500">{i.label}</dt>
          <dd className="mt-1 text-sm text-slate-900">{i.value ?? '—'}</dd>
        </div>
      ))}
    </dl>
  )
}
