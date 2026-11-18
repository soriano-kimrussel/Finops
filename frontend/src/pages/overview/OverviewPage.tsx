import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ArrowDownLeft, ArrowUpRight, RefreshCw } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { MoneyBarChart } from '@/components/charts'
import { BudgetMeter, Money } from '@/components/data'
import { DateRangeControl, effectiveRange } from '@/components/DateRange'
import { Alert, Badge, Card, CardHeader, EmptyState, ErrorState, PageHeader, Spinner, StatCard } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { formatDate, formatDateTime, formatMoney, humanize, isNegative, isZero } from '@/lib/format'
import type { BudgetLevel } from '@/lib/models'

type DashboardData = {
  period: { from: string; to: string }
  currency: string
  totals: {
    revenue: string
    expenses: string
    net_income: string
    cash_in: string
    cash_out: string
    net_cash_flow: string
    outstanding_amount: string
    outstanding_count: number
    overdue_amount: string
    overdue_count: number
  }
  other_currency_records: number
  monthly: { month: string; revenue: string; expenses: string; cash_in: string; cash_out: string; net_cash: string; net_income: string }[]
  budgets: { project_id: string; name: string; status: string; budget: string; actual: string; pending: string; percent_used: string; level: BudgetLevel }[]
  recent_transactions: { id: string; occurred_on: string; direction: 'in' | 'out'; amount: string; currency: string; description: string; source_type: string }[]
  recent_sync: { id: string; kind: string; status: string; mode: string; created_at: string; finished_at: string | null; stats: Record<string, number> | null; last_error: string | null }[]
}

export function OverviewPage() {
  const { orgId, organization, path, can } = useOrg()
  const [params, setParams] = useSearchParams()
  const range = { range: params.get('range') ?? 'last_12', from: params.get('from') ?? '', to: params.get('to') ?? '' }
  const { from, to } = effectiveRange(range)
  const query = useQuery({
    queryKey: orgKey(orgId, 'dashboard', from, to),
    queryFn: () => api<DashboardData>(`/orgs/${orgId}/dashboard${qs({ from, to })}`),
    placeholderData: keepPreviousData,
  })

  return (
    <>
      <PageHeader
        title="Overview"
        description={`Financial summary for ${organization.name}`}
        actions={
          <DateRangeControl
            preset={range.range}
            from={range.from || from}
            to={range.to || to}
            onChange={(v) => setParams(v.preset === 'custom' ? { range: v.preset, from: v.from, to: v.to } : { range: v.preset }, { replace: true })}
          />
        }
      />
      {query.isPending ? (
        <Spinner label="Loading dashboard" />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => query.refetch()} />
      ) : (
        <Dashboard data={query.data} path={path} canSeeSync={can('integrations.view')} />
      )}
    </>
  )
}

function Dashboard({ data, path, canSeeSync }: { data: DashboardData; path: (s: string) => string; canSeeSync: boolean }) {
  const cur = data.currency
  const t = data.totals
  return (
    <div className="space-y-6">
      {data.other_currency_records > 0 && (
        <Alert tone="blue">
          Figures are in {cur}. {data.other_currency_records} records in other currencies are not included; see Reports for each currency.
        </Alert>
      )}
      <p className="text-xs text-slate-500">
        {formatDate(data.period.from)} – {formatDate(data.period.to)} · receivables are as of today
      </p>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard label="Revenue" value={formatMoney(t.revenue, cur)} hint="Invoiced, excluding tax" />
        <StatCard label="Expenses" value={formatMoney(t.expenses, cur)} hint="Approved expenses" />
        <StatCard label="Net income" value={formatMoney(t.net_income, cur)} tone={isNegative(t.net_income) ? 'negative' : 'positive'} />
        <StatCard
          label="Net cash flow"
          value={formatMoney(t.net_cash_flow, cur)}
          tone={isNegative(t.net_cash_flow) ? 'negative' : 'default'}
          hint={`${formatMoney(t.cash_in, cur)} in · ${formatMoney(t.cash_out, cur)} out`}
        />
        <Link to={path('invoices?status=outstanding')} className="block">
          <StatCard label="Outstanding invoices" value={formatMoney(t.outstanding_amount, cur)} hint={`${t.outstanding_count} open invoices`} />
        </Link>
        <Link to={path('invoices?status=overdue')} className="block">
          <StatCard label="Overdue" value={formatMoney(t.overdue_amount, cur)} tone={isZero(t.overdue_amount) ? 'default' : 'negative'} hint={`${t.overdue_count} past due`} />
        </Link>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader title="Revenue vs. expenses" description="Per month" />
          <div className="p-4">
            <MoneyBarChart
              ariaLabel="Monthly revenue and expenses"
              rows={data.monthly}
              xKey="month"
              currency={cur}
              series={[
                { key: 'revenue', label: 'Revenue' },
                { key: 'expenses', label: 'Expenses' },
              ]}
            />
          </div>
        </Card>
        <Card>
          <CardHeader title="Cash flow" description="Money received and paid out per month" />
          <div className="p-4">
            <MoneyBarChart
              ariaLabel="Monthly cash in and cash out"
              rows={data.monthly}
              xKey="month"
              currency={cur}
              series={[
                { key: 'cash_in', label: 'Cash in' },
                { key: 'cash_out', label: 'Cash out' },
              ]}
            />
          </div>
        </Card>
      </div>

      <div className="grid gap-6 lg:grid-cols-3">
        <Card>
          <CardHeader title="Budget vs. actual" action={<Link to={path('budgets')} className="text-sm font-medium text-brand-700">All budgets</Link>} />
          {data.budgets.length === 0 ? (
            <EmptyState title="No budgeted projects" />
          ) : (
            <ul className="space-y-4 p-5">
              {data.budgets.map((b) => (
                <li key={b.project_id}>
                  <div className="mb-1 flex items-center justify-between gap-2">
                    <Link to={path(`projects/${b.project_id}`)} className="truncate text-sm font-medium text-slate-900 hover:text-brand-700">
                      {b.name}
                    </Link>
                    {b.level === 'over' && <Badge tone="red">Over</Badge>}
                    {b.level === 'warning' && <Badge tone="amber">Near limit</Badge>}
                  </div>
                  <BudgetMeter summary={{ total: b.budget, actual: b.actual, pending: b.pending, remaining: '', percent_used: b.percent_used, level: b.level }} currency={cur} compact />
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card>
          <CardHeader title="Recent transactions" description="Cash ledger" />
          {data.recent_transactions.length === 0 ? (
            <EmptyState title="No cash movements in this period" />
          ) : (
            <ul className="divide-y divide-slate-100">
              {data.recent_transactions.map((tx) => (
                <li key={tx.id} className="flex items-center gap-3 px-5 py-3 text-sm">
                  <span className={`rounded-full p-1.5 ${tx.direction === 'in' ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 text-slate-600'}`}>
                    {tx.direction === 'in' ? <ArrowDownLeft className="size-3.5" aria-label="Money in" /> : <ArrowUpRight className="size-3.5" aria-label="Money out" />}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-medium text-slate-900">{tx.description}</p>
                    <p className="text-xs text-slate-500">
                      {formatDate(tx.occurred_on)} · {humanize(tx.source_type)}
                    </p>
                  </div>
                  <Money amount={tx.direction === 'out' ? `-${tx.amount}` : tx.amount} currency={tx.currency} className={`font-medium ${tx.direction === 'in' ? 'text-emerald-700' : 'text-slate-900'}`} />
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card>
          <CardHeader title="Sync activity" action={canSeeSync ? <Link to={path('integrations')} className="text-sm font-medium text-brand-700">Integrations</Link> : undefined} />
          {!canSeeSync ? (
            <p className="p-5 text-sm text-slate-500">Your role does not include integration access.</p>
          ) : data.recent_sync.length === 0 ? (
            <EmptyState icon={<RefreshCw className="size-6" />} title="No synchronization yet" description="Connect QuickBooks on the Integrations page to import records." />
          ) : (
            <ul className="divide-y divide-slate-100">
              {data.recent_sync.map((j) => (
                <li key={j.id} className="px-5 py-3 text-sm">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium text-slate-900">{humanize(j.kind)} import</span>
                    <Badge tone={j.status === 'succeeded' ? 'green' : j.status === 'failed' ? 'red' : j.status === 'running' ? 'blue' : 'amber'}>{humanize(j.status)}</Badge>
                  </div>
                  <p className="text-xs text-slate-500">
                    {formatDateTime(j.finished_at ?? j.created_at)}
                    {j.mode === 'simulated' && ' · simulated data'}
                  </p>
                  {j.last_error && <p className="mt-1 truncate text-xs text-red-600">{j.last_error}</p>}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  )
}
