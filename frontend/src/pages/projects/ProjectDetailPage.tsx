import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowLeft, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { BudgetMeter, BudgetWarning, ExpenseStatusBadge, InvoiceStatusBadge, Money, ProjectStatusBadge } from '@/components/data'
import { ConfirmDialog } from '@/components/Dialog'
import { Alert, Button, Card, CardHeader, EmptyState, ErrorState, FormError, PageHeader, Spinner, StatCard, Table, Td, Th } from '@/components/ui'
import { api } from '@/lib/api'
import { formatDate, formatMoney, isZero } from '@/lib/format'
import type { BudgetLine, Expense, InvoiceListItem, ListResponse, ProjectDetail } from '@/lib/models'
import { BudgetLineDialog, ProjectFormDialog } from './ProjectForms'

export function ProjectDetailPage() {
  const { id = '' } = useParams()
  const { orgId, can, path } = useOrg()
  const qc = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [lineDialog, setLineDialog] = useState<{ open: boolean; line?: BudgetLine }>({ open: false })
  const [deleting, setDeleting] = useState<BudgetLine | null>(null)

  const detail = useQuery({ queryKey: orgKey(orgId, 'projects', 'detail', id), queryFn: () => api<ProjectDetail>(`/orgs/${orgId}/projects/${id}`) })
  const expenses = useQuery({
    queryKey: orgKey(orgId, 'expenses', 'project', id),
    queryFn: () => api<ListResponse<Expense>>(`/orgs/${orgId}/expenses?project_id=${id}&limit=10`),
    enabled: can('expenses.view'),
  })
  const invoices = useQuery({
    queryKey: orgKey(orgId, 'invoices', 'project', id),
    queryFn: () => api<ListResponse<InvoiceListItem>>(`/orgs/${orgId}/invoices?project_id=${id}&limit=10`),
    enabled: can('invoices.view'),
  })
  const remove = useMutation({
    mutationFn: (lineId: string) => api(`/orgs/${orgId}/budgets/${lineId}`, { method: 'DELETE' }),
    onSuccess: () => {
      setDeleting(null)
      qc.invalidateQueries({ queryKey: orgKey(orgId, 'projects') })
    },
  })

  if (detail.isPending) return <Spinner />
  if (detail.error) return <ErrorState error={detail.error} onRetry={() => detail.refetch()} />
  const { project: p, budget_lines: lines, unbudgeted, billing } = detail.data
  const cur = p.currency
  const manageBudgets = can('budgets.manage')

  return (
    <>
      <Link to={path('projects')} className="mb-3 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-700">
        <ArrowLeft className="size-4" aria-hidden /> Projects
      </Link>
      <PageHeader
        title={p.name}
        description={`${p.customer_name}${p.code ? ` · ${p.code}` : ''} · ${formatDate(p.start_date)} – ${p.end_date ? formatDate(p.end_date) : 'ongoing'}`}
        actions={
          <>
            <ProjectStatusBadge status={p.status} />
            {can('projects.manage') && (
              <Button variant="secondary" onClick={() => setEditing(true)}>
                <Pencil className="size-4" aria-hidden /> Edit
              </Button>
            )}
          </>
        }
      />

      {p.budget.level === 'over' && (
        <div className="mb-4">
          <Alert>
            <span className="inline-flex items-center gap-2 font-medium">
              <AlertTriangle className="size-4" aria-hidden /> This project is over budget by {formatMoney(p.budget.remaining.replace('-', ''), cur)}.
            </span>
          </Alert>
        </div>
      )}
      {p.budget.level === 'warning' && (
        <div className="mb-4">
          <Alert tone="amber">
            <span className="inline-flex items-center gap-2 font-medium">
              <AlertTriangle className="size-4" aria-hidden /> {p.budget.percent_used}% of the budget is used. {formatMoney(p.budget.remaining, cur)} remains.
            </span>
          </Alert>
        </div>
      )}

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Budget" value={formatMoney(p.budget.total, cur)} hint={<BudgetMeter summary={p.budget} currency={cur} compact />} />
        <StatCard label="Approved spend" value={formatMoney(p.budget.actual, cur)} hint={`${formatMoney(p.budget.pending, cur)} awaiting approval`} />
        <StatCard label="Billed" value={formatMoney(billing.billed, cur)} hint={`${billing.invoice_count} invoices`} />
        <StatCard label="Collected" value={formatMoney(billing.collected, cur)} hint={`${formatMoney(billing.outstanding, cur)} outstanding`} tone="positive" />
      </div>

      <Card className="mb-6">
        <CardHeader
          title="Budget vs. actual"
          description="Approved expenses count against the budget. Pending expenses are shown separately."
          action={
            manageBudgets && (
              <Button size="sm" onClick={() => setLineDialog({ open: true })}>
                <Plus className="size-4" aria-hidden /> Add line
              </Button>
            )
          }
        />
        {lines.length === 0 ? (
          <EmptyState title="No budget set" description="Add a general budget or per-category lines to track spending against plan." />
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>Category</Th>
                <Th align="right">Budget</Th>
                <Th align="right">Actual</Th>
                <Th align="right">Pending</Th>
                <Th align="right">Remaining</Th>
                <Th>Used</Th>
                {manageBudgets && <Th />}
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {lines.map((l) => (
                <tr key={l.id}>
                  <Td className="font-medium text-slate-900">
                    {l.category_name} <BudgetWarning summary={l.consumption} />
                  </Td>
                  <Td align="right">
                    <Money amount={l.amount} currency={cur} />
                  </Td>
                  <Td align="right">
                    <Money amount={l.consumption.actual} currency={cur} />
                  </Td>
                  <Td align="right" className="text-slate-500">
                    <Money amount={l.consumption.pending} currency={cur} />
                  </Td>
                  <Td align="right">
                    <Money amount={l.consumption.remaining} currency={cur} className={l.consumption.level === 'over' ? 'text-red-600' : ''} />
                  </Td>
                  <Td>
                    <BudgetMeter summary={l.consumption} currency={cur} compact />
                  </Td>
                  {manageBudgets && (
                    <Td align="right">
                      <Button variant="ghost" size="sm" aria-label={`Edit ${l.category_name} budget`} onClick={() => setLineDialog({ open: true, line: l })}>
                        <Pencil className="size-4" />
                      </Button>
                      <Button variant="ghost" size="sm" aria-label={`Delete ${l.category_name} budget`} onClick={() => setDeleting(l)}>
                        <Trash2 className="size-4" />
                      </Button>
                    </Td>
                  )}
                </tr>
              ))}
            </tbody>
          </Table>
        )}
        {!isZero(unbudgeted.approved) || !isZero(unbudgeted.pending) ? (
          <p className="border-t border-slate-100 px-5 py-3 text-sm text-amber-700">
            {formatMoney(unbudgeted.approved, cur)} approved ({formatMoney(unbudgeted.pending, cur)} pending) was spent in categories without a budget line.
          </p>
        ) : null}
      </Card>

      <div className="grid gap-6 lg:grid-cols-2">
        {can('expenses.view') && (
          <Card>
            <CardHeader title="Recent expenses" action={<Link className="text-sm font-medium text-brand-700" to={path(`expenses?project_id=${p.id}`)}>View all</Link>} />
            {expenses.data?.data.length ? (
              <ul className="divide-y divide-slate-100">
                {expenses.data.data.map((e) => (
                  <li key={e.id} className="flex items-center justify-between gap-3 px-5 py-3 text-sm">
                    <div>
                      <p className="font-medium text-slate-900">{e.vendor_name}</p>
                      <p className="text-xs text-slate-500">
                        {e.category_name} · {formatDate(e.expense_date)}
                      </p>
                    </div>
                    <div className="flex items-center gap-3">
                      <ExpenseStatusBadge status={e.approval_status} />
                      <Money amount={e.amount} currency={e.currency} className="font-medium" />
                    </div>
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState title="No expenses yet" />
            )}
          </Card>
        )}
        {can('invoices.view') && (
          <Card>
            <CardHeader title="Invoices" />
            {invoices.data?.data.length ? (
              <ul className="divide-y divide-slate-100">
                {invoices.data.data.map((i) => (
                  <li key={i.id} className="flex items-center justify-between gap-3 px-5 py-3 text-sm">
                    <div>
                      <Link to={path(`invoices/${i.id}`)} className="font-medium text-slate-900 hover:text-brand-700">
                        {i.number}
                      </Link>
                      <p className="text-xs text-slate-500">Due {formatDate(i.due_date)}</p>
                    </div>
                    <div className="flex items-center gap-3">
                      <InvoiceStatusBadge status={i.display_status ?? i.status} />
                      <Money amount={i.total} currency={i.currency} className="font-medium" />
                    </div>
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState title="No invoices for this project" />
            )}
          </Card>
        )}
      </div>

      <ProjectFormDialog open={editing} onOpenChange={setEditing} project={p} />
      <BudgetLineDialog
        open={lineDialog.open}
        onOpenChange={(o) => setLineDialog((s) => ({ ...s, open: o }))}
        projectId={p.id}
        currency={cur}
        line={lineDialog.line}
        usedCategoryIds={lines.map((l) => l.category_id)}
      />
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title="Delete budget line"
        description={deleting ? `Remove the ${deleting.category_name} budget of ${formatMoney(deleting.amount, cur)}? The change is recorded in the audit log.` : undefined}
        confirmLabel="Delete"
        tone="danger"
        loading={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting.id)}
      >
        <FormError error={remove.error} />
      </ConfirmDialog>
    </>
  )
}
