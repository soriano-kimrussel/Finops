import { zodResolver } from '@hookform/resolvers/zod'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Plus, Trash2 } from 'lucide-react'
import { useMemo } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { z } from 'zod'
import { orgKey, useOrg } from '@/app/org'
import { Money } from '@/components/data'
import { Alert, Button, Card, CardHeader, ErrorState, Field, FormError, Input, PageHeader, Select, Spinner, Textarea, fieldError } from '@/components/ui'
import { api, ApiError } from '@/lib/api'
import { addDaysISO, todayISO } from '@/lib/format'
import { useDebounced } from '@/lib/hooks'
import type { Invoice, InvoicePreview, ListResponse, Project } from '@/lib/models'
import { useActiveCustomers } from '../projects/ProjectForms'

const decimal = /^\d{1,15}(\.\d{1,4})?$/

export const invoiceSchema = z
  .object({
    customer_id: z.string().min(1, 'Choose a customer'),
    project_id: z.string(),
    issue_date: z.string().min(1, 'Choose an issue date'),
    due_date: z.string().min(1, 'Choose a due date'),
    discount_type: z.enum(['none', 'percent', 'fixed']),
    discount_value: z.string().trim(),
    tax_rate: z.string().trim().regex(/^(\d{1,3})(\.\d{1,4})?$|^$/, 'Enter a percentage such as 8.25'),
    notes: z.string().max(2000),
    items: z
      .array(
        z.object({
          description: z.string().trim().min(1, 'Describe the item').max(500),
          quantity: z.string().trim().regex(decimal, 'Enter a quantity'),
          unit_price: z.string().trim().regex(decimal, 'Enter a price'),
        }),
      )
      .min(1, 'Add at least one line item'),
  })
  .refine((v) => v.due_date >= v.issue_date, { path: ['due_date'], message: 'Due date must be on or after the issue date' })
  .refine((v) => v.discount_type === 'none' || decimal.test(v.discount_value), { path: ['discount_value'], message: 'Enter a discount' })

export type InvoiceValues = z.infer<typeof invoiceSchema>

function toBody(v: InvoiceValues) {
  return {
    ...v,
    project_id: v.project_id || null,
    discount_value: v.discount_type === 'none' ? '0' : v.discount_value,
    tax_rate: v.tax_rate || '0',
    notes: v.notes || null,
  }
}

export function InvoiceEditorPage() {
  const { id } = useParams()
  const { orgId } = useOrg()
  const existing = useQuery({
    queryKey: orgKey(orgId, 'invoices', 'detail', id),
    queryFn: () => api<{ invoice: Invoice }>(`/orgs/${orgId}/invoices/${id}`),
    enabled: Boolean(id),
  })
  if (id && existing.isPending) return <Spinner />
  if (id && existing.error) return <ErrorState error={existing.error} onRetry={() => existing.refetch()} />
  return <InvoiceEditor invoice={existing.data?.invoice} />
}

function InvoiceEditor({ invoice }: { invoice?: Invoice }) {
  const { orgId, path, organization } = useOrg()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [params] = useSearchParams()
  const customers = useActiveCustomers()

  const today = todayISO()
  const form = useForm<InvoiceValues>({
    resolver: zodResolver(invoiceSchema),
    defaultValues: invoice
      ? {
          customer_id: invoice.customer_id,
          project_id: invoice.project_id ?? '',
          issue_date: invoice.issue_date,
          due_date: invoice.due_date,
          discount_type: invoice.discount_type,
          discount_value: invoice.discount_type === 'none' ? '' : invoice.discount_value,
          tax_rate: invoice.tax_rate === '0' ? '' : invoice.tax_rate,
          notes: invoice.notes ?? '',
          items: invoice.items.map((i) => ({ description: i.description, quantity: i.quantity, unit_price: i.unit_price })),
        }
      : {
          customer_id: params.get('customer_id') ?? '',
          project_id: '',
          issue_date: today,
          due_date: addDaysISO(today, 30),
          discount_type: 'none',
          discount_value: '',
          tax_rate: '',
          notes: '',
          items: [{ description: '', quantity: '1', unit_price: '' }],
        },
  })
  const items = useFieldArray({ control: form.control, name: 'items' })
  const watched = useWatch({ control: form.control })
  const customerId = watched.customer_id ?? ''
  const customer = customers.data?.data.find((c) => c.id === customerId)
  const currency = invoice?.currency ?? customer?.currency ?? organization.base_currency

  const projects = useQuery({
    queryKey: orgKey(orgId, 'projects', 'by-customer', customerId),
    queryFn: () => api<ListResponse<Project>>(`/orgs/${orgId}/projects?customer_id=${customerId}&limit=200`),
    enabled: Boolean(customerId),
  })

  const previewJSON = useDebounced(JSON.stringify(previewRequest(watched, currency)), 350)
  const previewBody = useMemo(() => JSON.parse(previewJSON) as ReturnType<typeof previewRequest>, [previewJSON])
  const previewQuery = useQuery({
    queryKey: orgKey(orgId, 'invoice-preview', previewBody),
    queryFn: ({ signal }) => api<InvoicePreview>(`/orgs/${orgId}/invoices/preview`, { method: 'POST', body: previewBody, signal }),
    enabled: previewBody !== null,
    placeholderData: keepPreviousData,
    retry: false,
  })
  const preview = previewBody === null ? null : (previewQuery.data ?? null)
  const previewError =
    previewBody !== null && previewQuery.error
      ? previewQuery.error instanceof ApiError
        ? (Object.values(previewQuery.error.fields)[0] ?? previewQuery.error.message)
        : 'Could not calculate totals'
      : null

  const save = useMutation({
    mutationFn: (v: InvoiceValues) =>
      invoice
        ? api<{ invoice: Invoice }>(`/orgs/${orgId}/invoices/${invoice.id}`, { method: 'PATCH', body: { ...toBody(v), currency, version: invoice.version } })
        : api<{ invoice: Invoice }>(`/orgs/${orgId}/invoices`, { method: 'POST', body: { ...toBody(v), currency } }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: orgKey(orgId, 'invoices') })
      navigate(path(`invoices/${res.invoice.id}`))
    },
  })

  const errors = form.formState.errors
  const err = (f: string) => fieldError(save.error, f)
  const submit = form.handleSubmit((v) => save.mutate(v))
  const lineTotal = (i: number) => {
    const valid = (watched.items ?? []).slice(0, i + 1).filter((l) => l && decimal.test(l.quantity ?? '') && decimal.test(l.unit_price ?? ''))
    const item = watched.items?.[i]
    if (!preview || !item || !decimal.test(item.quantity ?? '') || !decimal.test(item.unit_price ?? '')) return null
    return preview.line_totals[valid.length - 1] ?? null
  }

  return (
    <>
      <Link to={invoice ? path(`invoices/${invoice.id}`) : path('invoices')} className="mb-3 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-700">
        <ArrowLeft className="size-4" aria-hidden /> {invoice ? invoice.number : 'Invoices'}
      </Link>
      <PageHeader title={invoice ? `Edit ${invoice.number}` : 'New invoice'} description={`Amounts in ${currency}. Totals are calculated by the server.`} />
      {invoice?.status === 'sent' && (
        <div className="mb-4">
          <Alert tone="amber">This invoice has already been sent. Changes are recorded in the audit log.</Alert>
        </div>
      )}
      <form onSubmit={submit} noValidate className="grid gap-6 lg:grid-cols-3">
        <div className="space-y-6 lg:col-span-2">
          <Card>
            <CardHeader title="Details" />
            <div className="grid gap-4 p-5 sm:grid-cols-2">
              <Field label="Customer" error={errors.customer_id?.message ?? err('customer_id')}>
                {(p) => (
                  <Select {...p} {...form.register('customer_id', { onChange: () => form.setValue('project_id', '') })} disabled={Boolean(invoice && invoice.status !== 'draft')}>
                    <option value="">Choose a customer…</option>
                    {invoice && !customers.data?.data.some((c) => c.id === invoice.customer_id) && <option value={invoice.customer_id}>{invoice.customer_name}</option>}
                    {customers.data?.data.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name} ({c.currency})
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              <Field label="Project (optional)" error={err('project_id') ?? err('currency')}>
                {(p) => (
                  <Select {...p} {...form.register('project_id')} disabled={!customerId}>
                    <option value="">No project</option>
                    {projects.data?.data.map((pr) => (
                      <option key={pr.id} value={pr.id}>
                        {pr.name}
                      </option>
                    ))}
                  </Select>
                )}
              </Field>
              <Field label="Issue date" error={errors.issue_date?.message ?? err('issue_date')}>
                {(p) => <Input {...p} type="date" {...form.register('issue_date')} />}
              </Field>
              <Field label="Due date" error={errors.due_date?.message ?? err('due_date')}>
                {(p) => <Input {...p} type="date" {...form.register('due_date')} />}
              </Field>
            </div>
          </Card>

          <Card>
            <CardHeader
              title="Line items"
              action={
                <Button size="sm" variant="secondary" onClick={() => items.append({ description: '', quantity: '1', unit_price: '' })}>
                  <Plus className="size-4" aria-hidden /> Add line
                </Button>
              }
            />
            <div className="divide-y divide-slate-100">
              {items.fields.map((f, i) => {
                const ie = errors.items?.[i]
                const total = lineTotal(i)
                return (
                  <div key={f.id} className="grid gap-3 px-5 py-4 sm:grid-cols-12 sm:items-start">
                    <Field label="Description" className="sm:col-span-6" error={ie?.description?.message ?? err(`items.${i}.description`)}>
                      {(p) => <Input {...p} {...form.register(`items.${i}.description`)} />}
                    </Field>
                    <Field label="Qty" className="sm:col-span-2" error={ie?.quantity?.message ?? err(`items.${i}.quantity`)}>
                      {(p) => <Input {...p} inputMode="decimal" {...form.register(`items.${i}.quantity`)} />}
                    </Field>
                    <Field label="Unit price" className="sm:col-span-2" error={ie?.unit_price?.message ?? err(`items.${i}.unit_price`)}>
                      {(p) => <Input {...p} inputMode="decimal" placeholder="0.00" {...form.register(`items.${i}.unit_price`)} />}
                    </Field>
                    <div className="flex items-end justify-between gap-2 sm:col-span-2 sm:flex-col sm:items-end">
                      <span className="text-xs font-medium text-slate-500 sm:mt-1">Amount</span>
                      <span className="py-2 text-sm font-medium tabular-nums text-slate-900" data-testid={`line-total-${i}`}>
                        {total ? <Money amount={total} currency={currency} /> : '—'}
                      </span>
                      {items.fields.length > 1 && (
                        <Button variant="ghost" size="sm" aria-label={`Remove line ${i + 1}`} onClick={() => items.remove(i)}>
                          <Trash2 className="size-4" />
                        </Button>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
            {(errors.items?.message || errors.items?.root?.message || err('items')) && (
              <p className="px-5 pb-4 text-xs text-red-600" role="alert">
                {errors.items?.message ?? errors.items?.root?.message ?? err('items')}
              </p>
            )}
          </Card>

          <Card>
            <CardHeader title="Notes" />
            <div className="p-5">
              <Field label="Notes shown on the invoice">{(p) => <Textarea {...p} rows={3} {...form.register('notes')} />}</Field>
            </div>
          </Card>
        </div>

        <div className="space-y-6">
          <Card>
            <CardHeader title="Discount and tax" />
            <div className="space-y-4 p-5">
              <Field label="Discount">
                {(p) => (
                  <Select {...p} {...form.register('discount_type')}>
                    <option value="none">No discount</option>
                    <option value="percent">Percentage of subtotal</option>
                    <option value="fixed">Fixed amount</option>
                  </Select>
                )}
              </Field>
              {watched.discount_type !== 'none' && (
                <Field label={watched.discount_type === 'percent' ? 'Discount (%)' : `Discount (${currency})`} error={errors.discount_value?.message ?? err('discount_value')}>
                  {(p) => <Input {...p} inputMode="decimal" {...form.register('discount_value')} />}
                </Field>
              )}
              <Field label="Tax rate (%)" hint="Applied once to the discounted subtotal" error={errors.tax_rate?.message ?? err('tax_rate')}>
                {(p) => <Input {...p} inputMode="decimal" placeholder="0" {...form.register('tax_rate')} />}
              </Field>
            </div>
          </Card>

          <Card>
            <CardHeader title="Summary" />
            <dl className="space-y-2 p-5 text-sm" aria-live="polite">
              <Row label="Subtotal" value={preview?.subtotal} currency={currency} />
              <Row label="Discount" value={preview?.discount_total} currency={currency} />
              <Row label="Tax" value={preview?.tax_total} currency={currency} />
              <div className="border-t border-slate-200 pt-2">
                <Row label="Total" value={preview?.total} currency={currency} strong />
              </div>
              {previewError && <p className="text-xs text-red-600">{previewError}</p>}
            </dl>
          </Card>

          <FormError error={save.error instanceof ApiError && Object.keys(save.error.fields).length === 0 ? save.error : null} />
          <div className="flex gap-2">
            <Button type="submit" className="flex-1" loading={save.isPending}>
              {invoice ? 'Save changes' : 'Save as draft'}
            </Button>
          </div>
        </div>
      </form>
    </>
  )
}

// Builds the preview request from the current form state; null when no line can be priced yet.
type WatchedInvoice = {
  discount_type?: InvoiceValues['discount_type']
  discount_value?: string
  tax_rate?: string
  items?: ({ description?: string; quantity?: string; unit_price?: string } | undefined)[]
}

function previewRequest(v: WatchedInvoice, currency: string) {
  const lines = (v.items ?? []).filter(
    (i): i is { description?: string; quantity: string; unit_price: string } => Boolean(i) && decimal.test(i?.quantity ?? '') && decimal.test(i?.unit_price ?? ''),
  )
  if (lines.length === 0) return null
  return {
    currency,
    discount_type: v.discount_type ?? 'none',
    discount_value: v.discount_type && v.discount_type !== 'none' ? v.discount_value || '0' : '0',
    tax_rate: v.tax_rate || '0',
    items: lines.map((l) => ({ description: l.description || 'Item', quantity: l.quantity, unit_price: l.unit_price })),
  }
}

function Row({ label, value, currency, strong }: { label: string; value?: string; currency: string; strong?: boolean }) {
  return (
    <div className={`flex justify-between ${strong ? 'text-base font-semibold text-slate-900' : 'text-slate-600'}`}>
      <dt>{label}</dt>
      <dd data-testid={`summary-${label.toLowerCase()}`}>{value === undefined ? '—' : <Money amount={value} currency={currency} />}</dd>
    </div>
  )
}
