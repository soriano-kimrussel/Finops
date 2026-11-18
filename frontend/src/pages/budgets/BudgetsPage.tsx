import { useQuery } from '@tanstack/react-query'
import { PiggyBank } from 'lucide-react'
import { Link } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { BudgetMeter, BudgetWarning, Money, ProjectStatusBadge } from '@/components/data'
import { Card, EmptyState, ErrorState, PageHeader, Spinner, StatCard, Table, Td, Th } from '@/components/ui'
import { api } from '@/lib/api'
import type { BudgetOverview } from '@/lib/models'

export function BudgetsPage() {
  const { orgId, path } = useOrg()
  const query = useQuery({ queryKey: orgKey(orgId, 'budgets'), queryFn: () => api<{ data: BudgetOverview[] }>(`/orgs/${orgId}/budgets`) })

  return (
    <>
      <PageHeader title="Budgets" description="Budget versus approved spending for every budgeted project. Manage lines from the project page." />
      {query.isPending ? (
        <Spinner />
      ) : query.error ? (
        <ErrorState error={query.error} onRetry={() => query.refetch()} />
      ) : query.data.data.length === 0 ? (
        <Card>
          <EmptyState icon={<PiggyBank className="size-6" />} title="No budgets yet" description="Open a project and add budget lines to start tracking spend against plan." />
        </Card>
      ) : (
        <>
          <div className="mb-6 grid gap-4 sm:grid-cols-3">
            <StatCard label="Budgeted projects" value={query.data.data.length} />
            <StatCard label="Approaching budget" value={query.data.data.filter((b) => b.totals.level === 'warning').length} tone="warning" />
            <StatCard label="Over budget" value={query.data.data.filter((b) => b.totals.level === 'over').length} tone="negative" />
          </div>
          <div className="space-y-6">
            {query.data.data.map((b) => (
              <Card key={b.project_id}>
                <div className="flex flex-wrap items-center justify-between gap-4 border-b border-slate-100 px-5 py-4">
                  <div className="flex items-center gap-2">
                    <Link to={path(`projects/${b.project_id}`)} className="font-semibold text-slate-900 hover:text-brand-700">
                      {b.project_name}
                    </Link>
                    <ProjectStatusBadge status={b.project_status} />
                    <BudgetWarning summary={b.totals} />
                  </div>
                  <div className="w-64">
                    <BudgetMeter summary={b.totals} currency={b.currency} />
                  </div>
                </div>
                <Table>
                  <thead>
                    <tr>
                      <Th>Category</Th>
                      <Th align="right">Budget</Th>
                      <Th align="right">Actual</Th>
                      <Th align="right">Pending</Th>
                      <Th align="right">Remaining</Th>
                      <Th>Used</Th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-slate-100">
                    {b.lines.map((l) => (
                      <tr key={l.id}>
                        <Td className="font-medium text-slate-900">{l.category_name}</Td>
                        <Td align="right">
                          <Money amount={l.amount} currency={b.currency} />
                        </Td>
                        <Td align="right">
                          <Money amount={l.consumption.actual} currency={b.currency} />
                        </Td>
                        <Td align="right" className="text-slate-500">
                          <Money amount={l.consumption.pending} currency={b.currency} />
                        </Td>
                        <Td align="right">
                          <Money amount={l.consumption.remaining} currency={b.currency} className={l.consumption.level === 'over' ? 'text-red-600' : ''} />
                        </Td>
                        <Td>
                          <BudgetMeter summary={l.consumption} currency={b.currency} compact />
                        </Td>
                      </tr>
                    ))}
                  </tbody>
                </Table>
              </Card>
            ))}
          </div>
        </>
      )}
    </>
  )
}
