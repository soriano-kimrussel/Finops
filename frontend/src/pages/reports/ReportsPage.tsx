import { keepPreviousData, useQuery } from '@tanstack/react-query'
import clsx from 'clsx'
import { Download } from 'lucide-react'
import { useSearchParams } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { MoneyBarChart, type Series } from '@/components/charts'
import { DateRangeControl, effectiveRange } from '@/components/DateRange'
import { Card, CardHeader, EmptyState, ErrorState, PageHeader, Spinner, Table, Td, Th } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { formatDate, formatMoney, humanize } from '@/lib/format'

type Column = { key: string; label: string; kind: 'text' | 'money' | 'number' | 'percent' | 'date' | 'month' }
type Report = {
  key: string
  title: string
  description: string
  currency: string
  period: Record<string, string>
  columns: Column[]
  rows: Record<string, string>[]
  totals?: Record<string, string>
  notes?: string[]
}

const reports = [
  { key: 'profit-and-loss', label: 'Profit & loss' },
  { key: 'revenue', label: 'Revenue' },
  { key: 'expenses', label: 'Expenses' },
  { key: 'cash-flow', label: 'Cash flow' },
  { key: 'accounts-receivable', label: 'AR aging' },
  { key: 'outstanding-invoices', label: 'Outstanding' },
  { key: 'budget-vs-actual', label: 'Budget vs. actual' },
  { key: 'monthly-summary', label: 'Monthly summary' },
]

const asOfReports = new Set(['accounts-receivable', 'outstanding-invoices', 'budget-vs-actual'])

const charts: Record<string, { x: string; series: Series[]; horizontal?: boolean; title: string } | undefined> = {
  revenue: { x: 'customer', series: [{ key: 'revenue', label: 'Revenue' }], horizontal: true, title: 'Revenue by customer' },
  expenses: { x: 'category', series: [{ key: 'total', label: 'Expenses' }], horizontal: true, title: 'Approved expenses by category' },
  'cash-flow': { x: 'month', series: [{ key: 'cash_in', label: 'Cash in' }, { key: 'cash_out', label: 'Cash out' }], title: 'Cash in vs. cash out' },
  'monthly-summary': { x: 'month', series: [{ key: 'revenue', label: 'Revenue' }, { key: 'expenses', label: 'Expenses' }], title: 'Revenue vs. expenses' },
  'budget-vs-actual': { x: 'project', series: [{ key: 'budget', label: 'Budget' }, { key: 'actual', label: 'Actual' }], horizontal: true, title: 'Budget vs. actual by project' },
}

function cell(value: string | undefined, col: Column, currency: string) {
  if (value === undefined || value === '') return ''
  switch (col.kind) {
    case 'money':
      return formatMoney(value, currency)
    case 'percent':
      return `${value}%`
    case 'date':
      return formatDate(value)
    case 'month': {
      const [y, m] = value.split('-')
      return y && m ? new Date(Date.UTC(Number(y), Number(m) - 1, 1)).toLocaleString('en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' }) : value
    }
    default:
      return col.key === 'level' || col.key === 'status' ? humanize(value) : value
  }
}

export function ReportsPage() {
  const { orgId } = useOrg()
  const [params, setParams] = useSearchParams()
  const key = params.get('report') ?? 'profit-and-loss'
  const range = { range: params.get('range') ?? 'last_12', from: params.get('from') ?? '', to: params.get('to') ?? '' }
  const { from, to } = effectiveRange(range)
  const update = (next: Record<string, string>) => {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    setParams(p, { replace: true })
  }

  const query = useQuery({
    queryKey: orgKey(orgId, 'reports', key, from, to),
    queryFn: () => api<Report>(`/orgs/${orgId}/reports/${key}${qs({ from, to })}`),
    placeholderData: keepPreviousData,
  })
  const csvHref = `/api/orgs/${orgId}/reports/${key}${qs({ from, to, format: 'csv' })}`
  const chart = charts[key]

  return (
    <>
      <PageHeader
        title="Reports"
        description="Aggregated in the database from your financial records"
        actions={
          <>
            {!asOfReports.has(key) && (
              <DateRangeControl
                preset={range.range}
                from={range.from || from}
                to={range.to || to}
                onChange={(v) => update(v.preset === 'custom' ? { range: v.preset, from: v.from, to: v.to } : { range: v.preset, from: '', to: '' })}
              />
            )}
            <a
              href={csvHref}
              download
              className="inline-flex h-9 items-center gap-2 rounded-md border border-slate-300 bg-white px-4 text-sm font-medium text-slate-700 shadow-sm hover:bg-slate-50"
            >
              <Download className="size-4" aria-hidden /> Export CSV
            </a>
          </>
        }
      />
      <div role="tablist" aria-label="Report" className="mb-6 flex flex-wrap gap-1 rounded-lg border border-slate-200 bg-white p-1">
        {reports.map((r) => (
          <button
            key={r.key}
            role="tab"
            aria-selected={r.key === key}
            onClick={() => update({ report: r.key })}
            className={clsx('rounded-md px-3 py-1.5 text-sm font-medium', r.key === key ? 'bg-brand-600 text-white' : 'text-slate-600 hover:bg-slate-100')}
          >
            {r.label}
          </button>
        ))}
      </div>

      {query.isPending ? (
        <Spinner label="Building report" />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => query.refetch()} />
      ) : (
        <div className="space-y-6">
          {chart && query.data.rows.length > 0 && (
            <Card>
              <CardHeader title={chart.title} />
              <div className="p-4">
                <MoneyBarChart
                  ariaLabel={chart.title}
                  rows={query.data.rows.slice(0, chart.horizontal ? 12 : undefined)}
                  xKey={chart.x}
                  series={chart.series}
                  currency={query.data.currency}
                  horizontal={chart.horizontal}
                  height={chart.horizontal ? Math.max(180, Math.min(12, query.data.rows.length) * 34) : 260}
                />
              </div>
            </Card>
          )}
          <ReportTable report={query.data} />
        </div>
      )}
    </>
  )
}

function ReportTable({ report }: { report: Report }) {
  const period = report.period.as_of ? `As of ${formatDate(report.period.as_of)}` : `${formatDate(report.period.from)} – ${formatDate(report.period.to)}`
  return (
    <Card>
      <CardHeader title={report.title} description={`${report.description} ${period}. Amounts in ${report.currency}.`} />
      {report.rows.length === 0 ? (
        <EmptyState title="Nothing to report for this period" />
      ) : (
        <Table>
          <thead>
            <tr>
              {report.columns.map((c) => (
                <Th key={c.key} align={c.kind === 'text' || c.kind === 'date' || c.kind === 'month' ? 'left' : 'right'}>
                  {c.label}
                </Th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {report.rows.map((r, i) => (
              <tr
                key={i}
                className={clsx(r._kind === 'heading' && 'bg-slate-50', (r._kind === 'total' || r._kind === 'grand') && 'font-semibold', r._kind === 'grand' && 'border-t-2 border-slate-300')}
              >
                {report.columns.map((c, ci) => (
                  <Td
                    key={c.key}
                    align={c.kind === 'text' || c.kind === 'date' || c.kind === 'month' ? 'left' : 'right'}
                    className={clsx(r._kind === 'line' && ci === 0 && 'pl-8', r.level === 'over' && c.key === 'level' && 'text-red-600', r.level === 'warning' && c.key === 'level' && 'text-amber-600')}
                  >
                    {cell(r[c.key], c, report.currency)}
                  </Td>
                ))}
              </tr>
            ))}
          </tbody>
          {report.totals && report.key !== 'profit-and-loss' && (
            <tfoot className="border-t-2 border-slate-200 bg-slate-50 font-semibold">
              <tr>
                {report.columns.map((c) => (
                  <Td key={c.key} align={c.kind === 'text' || c.kind === 'date' || c.kind === 'month' ? 'left' : 'right'} className="text-slate-900">
                    {c.kind === 'month' ? report.totals?.[c.key] : cell(report.totals?.[c.key], c, report.currency)}
                  </Td>
                ))}
              </tr>
            </tfoot>
          )}
        </Table>
      )}
      {report.notes?.map((n) => (
        <p key={n} className="border-t border-slate-100 px-5 py-3 text-xs text-slate-500">
          {n}
        </p>
      ))}
    </Card>
  )
}
