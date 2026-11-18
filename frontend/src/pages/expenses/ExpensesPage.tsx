import { zodResolver } from '@hookform/resolvers/zod'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Pencil, Plus, Receipt, Trash2, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { useMe } from '@/app/auth'
import { orgKey, useOrg } from '@/app/org'
import { DateFilter, ExpenseStatusBadge, FilterBar, FilterSelect, Money, Pagination, SearchBox } from '@/components/data'
import { ConfirmDialog, Dialog } from '@/components/Dialog'
import { Button, Card, EmptyState, ErrorState, Field, FormError, Input, PageHeader, Select, Spinner, Table, Td, Textarea, Th, fieldError } from '@/components/ui'
import { api, ApiError, qs } from '@/lib/api'
import { formatDate, formatMoney, todayISO } from '@/lib/format'
import { useFilters } from '@/lib/hooks'
import type { Expense, ListResponse, Project } from '@/lib/models'
import { useCategories } from '../projects/ProjectForms'

const LIMIT = 25

type ExpenseList = ListResponse<Expense> & { total_amount: string; currency: string }

export function ExpensesPage() {
  const { orgId, can } = useOrg()
  const me = useMe()
  const qc = useQueryClient()
  const { values, set } = useFilters(['q', 'status', 'category_id', 'project_id', 'from', 'to', 'offset'] as const)
  const offset = Number(values.offset) || 0
  const categories = useCategories()
  const projects = useQuery({ queryKey: orgKey(orgId, 'projects', 'options'), queryFn: () => api<ListResponse<Project>>(`/orgs/${orgId}/projects?limit=200`) })
  const [editor, setEditor] = useState<{ open: boolean; expense?: Expense }>({ open: false })
  const [review, setReview] = useState<{ expense: Expense; approve: boolean } | null>(null)
  const [note, setNote] = useState('')
  const [deleting, setDeleting] = useState<Expense | null>(null)

  const query = useQuery({
    queryKey: orgKey(orgId, 'expenses', values),
    queryFn: () =>
      api<ExpenseList>(
        `/orgs/${orgId}/expenses${qs({ q: values.q, status: values.status, category_id: values.category_id, project_id: values.project_id, from: values.from, to: values.to, limit: LIMIT, offset })}`,
      ),
    placeholderData: keepPreviousData,
  })
  const invalidate = () => qc.invalidateQueries({ queryKey: orgKey(orgId) })
  const reviewMut = useMutation({
    mutationFn: (r: { expense: Expense; approve: boolean }) =>
      api(`/orgs/${orgId}/expenses/${r.expense.id}/${r.approve ? 'approve' : 'reject'}`, { method: 'POST', body: { note: note || null } }),
    onSuccess: () => {
      setReview(null)
      setNote('')
      invalidate()
    },
  })
  const deleteMut = useMutation({
    mutationFn: (e: Expense) => api(`/orgs/${orgId}/expenses/${e.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      setDeleting(null)
      invalidate()
    },
  })

  const userId = me.data?.user.id
  const canChange = (e: Expense) => e.approval_status === 'pending' && (e.submitted_by === userId || can('expenses.approve'))
  const canReview = (e: Expense) => e.approval_status === 'pending' && can('expenses.approve')

  return (
    <>
      <PageHeader
        title="Expenses"
        description="Spending submitted by your team. Only approved expenses count toward budgets and reports."
        actions={
          can('expenses.submit') && (
            <Button onClick={() => setEditor({ open: true })}>
              <Plus className="size-4" aria-hidden /> New expense
            </Button>
          )
        }
      />
      <Card>
        <FilterBar>
          <SearchBox value={values.q} onChange={(v) => set('q', v)} placeholder="Search vendor or description" label="Search expenses" />
          <FilterSelect
            label="Status"
            value={values.status}
            onChange={(v) => set('status', v)}
            options={[
              { value: 'pending', label: 'Pending' },
              { value: 'approved', label: 'Approved' },
              { value: 'rejected', label: 'Rejected' },
            ]}
          />
          <FilterSelect label="Category" value={values.category_id} onChange={(v) => set('category_id', v)} options={(categories.data?.data ?? []).map((c) => ({ value: c.id, label: c.name }))} />
          <FilterSelect label="Project" value={values.project_id} onChange={(v) => set('project_id', v)} options={(projects.data?.data ?? []).map((p) => ({ value: p.id, label: p.name }))} />
          <DateFilter label="From" value={values.from} onChange={(v) => set('from', v)} />
          <DateFilter label="to" value={values.to} onChange={(v) => set('to', v)} />
        </FilterBar>
        {query.isPending ? (
          <Spinner />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        ) : query.data.data.length === 0 ? (
          <EmptyState icon={<Receipt className="size-6" />} title="No expenses found" description="Submitted expenses appear here for review." />
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <Th>Date</Th>
                  <Th>Vendor</Th>
                  <Th>Category</Th>
                  <Th>Project</Th>
                  <Th>Submitted by</Th>
                  <Th>Status</Th>
                  <Th align="right">Amount</Th>
                  <Th />
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {query.data.data.map((e) => (
                  <tr key={e.id} className="hover:bg-slate-50">
                    <Td>{formatDate(e.expense_date)}</Td>
                    <Td>
                      <div className="font-medium text-slate-900">{e.vendor_name}</div>
                      {e.description && <div className="max-w-xs truncate text-xs text-slate-500">{e.description}</div>}
                    </Td>
                    <Td>{e.category_name}</Td>
                    <Td>{e.project_name ?? '—'}</Td>
                    <Td>{e.submitted_by_name ?? '—'}</Td>
                    <Td>
                      <ExpenseStatusBadge status={e.approval_status} />
                    </Td>
                    <Td align="right">
                      <Money amount={e.amount} currency={e.currency} className="font-medium text-slate-900" />
                    </Td>
                    <Td align="right">
                      <div className="flex justify-end gap-1">
                        {canReview(e) && (
                          <>
                            <Button variant="ghost" size="sm" aria-label={`Approve ${e.vendor_name}`} onClick={() => setReview({ expense: e, approve: true })}>
                              <Check className="size-4 text-emerald-600" />
                            </Button>
                            <Button variant="ghost" size="sm" aria-label={`Reject ${e.vendor_name}`} onClick={() => setReview({ expense: e, approve: false })}>
                              <X className="size-4 text-red-600" />
                            </Button>
                          </>
                        )}
                        {canChange(e) && (
                          <>
                            <Button variant="ghost" size="sm" aria-label={`Edit ${e.vendor_name}`} onClick={() => setEditor({ open: true, expense: e })}>
                              <Pencil className="size-4" />
                            </Button>
                            <Button variant="ghost" size="sm" aria-label={`Delete ${e.vendor_name}`} onClick={() => setDeleting(e)}>
                              <Trash2 className="size-4" />
                            </Button>
                          </>
                        )}
                      </div>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <div className="flex justify-end border-t border-slate-100 px-4 py-3 text-sm text-slate-600">
              Total of matching expenses in {query.data.currency}:&nbsp;<strong className="tabular-nums text-slate-900">{formatMoney(query.data.total_amount, query.data.currency)}</strong>
            </div>
            <Pagination total={query.data.total} limit={LIMIT} offset={offset} onChange={(o) => set('offset', String(o))} />
          </>
        )}
      </Card>

      <ExpenseDialog open={editor.open} expense={editor.expense} onOpenChange={(o) => setEditor((s) => ({ ...s, open: o }))} projects={projects.data?.data ?? []} />
      <ConfirmDialog
        open={review !== null}
        onOpenChange={(o) => !o && setReview(null)}
        title={review?.approve ? 'Approve expense' : 'Reject expense'}
        description={
          review
            ? `${review.expense.vendor_name}, ${formatMoney(review.expense.amount, review.expense.currency)} on ${formatDate(review.expense.expense_date)}. ${review.approve ? 'Approved expenses are locked and count toward budgets and reports.' : ''}`
            : undefined
        }
        confirmLabel={review?.approve ? 'Approve' : 'Reject'}
        tone={review?.approve ? 'primary' : 'danger'}
        loading={reviewMut.isPending}
        onConfirm={() => review && reviewMut.mutate(review)}
      >
        <div className="space-y-3">
          <Field label={review?.approve ? 'Note (optional)' : 'Reason for rejection'} error={fieldError(reviewMut.error, 'note')}>
            {(p) => <Textarea {...p} rows={2} value={note} onChange={(ev) => setNote(ev.target.value)} />}
          </Field>
          {reviewMut.error && !fieldError(reviewMut.error, 'note') && <FormError error={reviewMut.error} />}
        </div>
      </ConfirmDialog>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title="Delete expense"
        description={deleting ? `Delete the pending ${formatMoney(deleting.amount, deleting.currency)} expense from ${deleting.vendor_name}?` : undefined}
        confirmLabel="Delete"
        tone="danger"
        loading={deleteMut.isPending}
        onConfirm={() => deleting && deleteMut.mutate(deleting)}
      >
        <FormError error={deleteMut.error} />
      </ConfirmDialog>
    </>
  )
}

const expenseSchema = z.object({
  vendor_name: z.string().trim().min(1, 'Enter the vendor').max(200),
  category_id: z.string().min(1, 'Choose a category'),
  project_id: z.string(),
  expense_date: z.string().min(1, 'Choose a date'),
  amount: z.string().trim().regex(/^\d{1,15}(\.\d{1,4})?$/, 'Enter an amount such as 49.99'),
  description: z.string().max(1000),
})
type ExpenseValues = z.infer<typeof expenseSchema>

function ExpenseDialog({ open, onOpenChange, expense, projects }: { open: boolean; onOpenChange: (o: boolean) => void; expense?: Expense; projects: Project[] }) {
  const { orgId, organization } = useOrg()
  const qc = useQueryClient()
  const categories = useCategories()
  const vendors = useQuery({
    queryKey: orgKey(orgId, 'vendors'),
    queryFn: () => api<{ data: { id: string; name: string }[] }>(`/orgs/${orgId}/vendors`),
    enabled: open,
    staleTime: 60_000,
  })
  const defaults = (): ExpenseValues => ({
    vendor_name: expense?.vendor_name ?? '',
    category_id: expense?.category_id ?? '',
    project_id: expense?.project_id ?? '',
    expense_date: expense?.expense_date ?? todayISO(),
    amount: expense?.amount ?? '',
    description: expense?.description ?? '',
  })
  const form = useForm<ExpenseValues>({ resolver: zodResolver(expenseSchema), defaultValues: defaults() })
  useEffect(() => {
    if (open) form.reset(defaults())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, expense])
  const projectId = useWatch({ control: form.control, name: 'project_id' })
  const currency = projects.find((p) => p.id === projectId)?.currency ?? organization.base_currency

  const save = useMutation({
    mutationFn: (v: ExpenseValues) => {
      const body = { ...v, project_id: v.project_id || null, description: v.description || null, currency, version: expense?.version }
      return expense ? api(`/orgs/${orgId}/expenses/${expense.id}`, { method: 'PATCH', body }) : api(`/orgs/${orgId}/expenses`, { method: 'POST', body })
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: orgKey(orgId) })
      onOpenChange(false)
    },
  })
  const err = (f: keyof ExpenseValues) => form.formState.errors[f]?.message ?? fieldError(save.error, f)
  const submit = form.handleSubmit((v) => save.mutate(v))

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={expense ? 'Edit expense' : 'New expense'}
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} loading={save.isPending}>
            {expense ? 'Save changes' : 'Submit expense'}
          </Button>
        </>
      }
    >
      <form onSubmit={submit} className="grid gap-4 sm:grid-cols-2" noValidate>
        {save.error instanceof ApiError && Object.keys(save.error.fields).length === 0 && (
          <div className="sm:col-span-2">
            <FormError error={save.error} />
          </div>
        )}
        <Field label="Vendor" hint="Pick an existing vendor or type a new one" error={err('vendor_name')} className="sm:col-span-2">
          {(p) => <Input {...p} list="vendor-options" autoComplete="off" {...form.register('vendor_name')} />}
        </Field>
        <datalist id="vendor-options">
          {(vendors.data?.data ?? []).map((v) => (
            <option key={v.id} value={v.name} />
          ))}
        </datalist>
        <Field label="Category" error={err('category_id')}>
          {(p) => (
            <Select {...p} {...form.register('category_id')}>
              <option value="">Choose…</option>
              {(categories.data?.data ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Project (optional)" error={err('project_id') ?? fieldError(save.error, 'currency')}>
          {(p) => (
            <Select {...p} {...form.register('project_id')}>
              <option value="">No project</option>
              {projects.map((pr) => (
                <option key={pr.id} value={pr.id}>
                  {pr.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Date" error={err('expense_date')}>
          {(p) => <Input {...p} type="date" max={todayISO()} {...form.register('expense_date')} />}
        </Field>
        <Field label={`Amount (${currency})`} error={err('amount')}>
          {(p) => <Input {...p} inputMode="decimal" placeholder="0.00" {...form.register('amount')} />}
        </Field>
        <Field label="Description" error={err('description')} className="sm:col-span-2">
          {(p) => <Textarea {...p} rows={2} {...form.register('description')} />}
        </Field>
      </form>
    </Dialog>
  )
}
