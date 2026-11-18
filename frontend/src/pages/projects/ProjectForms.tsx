import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { orgKey, useOrg } from '@/app/org'
import { Dialog } from '@/components/Dialog'
import { Button, Field, FormError, Input, Select, Textarea, fieldError } from '@/components/ui'
import { api, ApiError } from '@/lib/api'
import { todayISO } from '@/lib/format'
import type { BudgetLine, Customer, ExpenseCategory, ListResponse, Project } from '@/lib/models'

const projectSchema = z
  .object({
    customer_id: z.string().min(1, 'Choose a customer'),
    name: z.string().trim().min(1, 'Enter a project name').max(200),
    code: z.string().trim().max(40).optional().or(z.literal('')),
    description: z.string().max(2000).optional().or(z.literal('')),
    status: z.enum(['planned', 'active', 'on_hold', 'completed', 'cancelled']),
    start_date: z.string().min(1, 'Choose a start date'),
    end_date: z.string().optional().or(z.literal('')),
  })
  .refine((v) => !v.end_date || v.end_date >= v.start_date, { path: ['end_date'], message: 'End date must be on or after the start date' })

type ProjectValues = z.infer<typeof projectSchema>

export function useActiveCustomers() {
  const { orgId } = useOrg()
  return useQuery({
    queryKey: orgKey(orgId, 'customers', 'options'),
    queryFn: () => api<ListResponse<Customer>>(`/orgs/${orgId}/customers?status=active&limit=200`),
    staleTime: 30_000,
  })
}

export function useCategories() {
  const { orgId } = useOrg()
  return useQuery({
    queryKey: orgKey(orgId, 'expense-categories'),
    queryFn: () => api<{ data: ExpenseCategory[] }>(`/orgs/${orgId}/expense-categories`),
    staleTime: 60_000,
  })
}

export function ProjectFormDialog({
  open,
  onOpenChange,
  project,
  onSaved,
  defaultCustomerId,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  project?: Project
  onSaved?: (p: Project) => void
  defaultCustomerId?: string
}) {
  const { orgId } = useOrg()
  const qc = useQueryClient()
  const customers = useActiveCustomers()
  const defaults = (): ProjectValues => ({
    customer_id: project?.customer_id ?? defaultCustomerId ?? '',
    name: project?.name ?? '',
    code: project?.code ?? '',
    description: project?.description ?? '',
    status: project?.status ?? 'active',
    start_date: project?.start_date ?? todayISO(),
    end_date: project?.end_date ?? '',
  })
  const form = useForm<ProjectValues>({ resolver: zodResolver(projectSchema), defaultValues: defaults() })
  useEffect(() => {
    if (open) form.reset(defaults())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, project])

  const save = useMutation({
    mutationFn: (v: ProjectValues) => {
      const body = { ...v, end_date: v.end_date || null }
      return project
        ? api<{ project: Project }>(`/orgs/${orgId}/projects/${project.id}`, { method: 'PATCH', body })
        : api<{ project: Project }>(`/orgs/${orgId}/projects`, { method: 'POST', body })
    },
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: orgKey(orgId, 'projects') })
      onOpenChange(false)
      onSaved?.(res.project)
    },
  })
  const err = (f: keyof ProjectValues) => form.formState.errors[f]?.message ?? fieldError(save.error, f)
  const submit = form.handleSubmit((v) => save.mutate(v))
  const options = customers.data?.data ?? []

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={project ? 'Edit project' : 'New project'}
      wide
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} loading={save.isPending}>
            {project ? 'Save changes' : 'Create project'}
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
        <Field label="Project name" error={err('name')}>
          {(p) => <Input {...p} {...form.register('name')} />}
        </Field>
        <Field label="Code" hint="Optional short reference, e.g. WEB-24" error={err('code')}>
          {(p) => <Input {...p} {...form.register('code')} />}
        </Field>
        <Field label="Customer" error={err('customer_id')}>
          {(p) => (
            <Select {...p} {...form.register('customer_id')}>
              <option value="">Choose a customer…</option>
              {project && !options.some((c) => c.id === project.customer_id) && <option value={project.customer_id}>{project.customer_name}</option>}
              {options.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({c.currency})
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Status" error={err('status')}>
          {(p) => (
            <Select {...p} {...form.register('status')}>
              <option value="planned">Planned</option>
              <option value="active">Active</option>
              <option value="on_hold">On hold</option>
              <option value="completed">Completed</option>
              <option value="cancelled">Cancelled</option>
            </Select>
          )}
        </Field>
        <Field label="Start date" error={err('start_date')}>
          {(p) => <Input {...p} type="date" {...form.register('start_date')} />}
        </Field>
        <Field label="End date" error={err('end_date')}>
          {(p) => <Input {...p} type="date" {...form.register('end_date')} />}
        </Field>
        <Field label="Description" error={err('description')} className="sm:col-span-2">
          {(p) => <Textarea {...p} rows={3} {...form.register('description')} />}
        </Field>
        {!project && <p className="text-xs text-slate-500 sm:col-span-2">The project uses the customer's billing currency. Budgets and expenses for the project must use the same currency.</p>}
      </form>
    </Dialog>
  )
}

const amountPattern = /^\d{1,15}(\.\d{1,4})?$/

export function BudgetLineDialog({
  open,
  onOpenChange,
  projectId,
  currency,
  line,
  usedCategoryIds,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  projectId: string
  currency: string
  line?: BudgetLine
  usedCategoryIds: (string | null)[]
}) {
  const { orgId } = useOrg()
  const qc = useQueryClient()
  const categories = useCategories()
  const schema = z.object({
    category_id: z.string(),
    amount: z.string().trim().regex(amountPattern, 'Enter an amount such as 5000.00'),
    notes: z.string().max(500).optional().or(z.literal('')),
  })
  type V = z.infer<typeof schema>
  const form = useForm<V>({ resolver: zodResolver(schema), defaultValues: { category_id: line?.category_id ?? '', amount: line?.amount ?? '', notes: line?.notes ?? '' } })
  useEffect(() => {
    if (open) form.reset({ category_id: line?.category_id ?? '', amount: line?.amount ?? '', notes: line?.notes ?? '' })
  }, [open, line, form])

  const save = useMutation({
    mutationFn: (v: V) =>
      line
        ? api(`/orgs/${orgId}/budgets/${line.id}`, { method: 'PATCH', body: { amount: v.amount, notes: v.notes || null, version: line.version } })
        : api(`/orgs/${orgId}/budgets`, { method: 'POST', body: { project_id: projectId, category_id: v.category_id || null, amount: v.amount, notes: v.notes || null } }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: orgKey(orgId, 'projects') })
      qc.invalidateQueries({ queryKey: orgKey(orgId, 'budgets') })
      onOpenChange(false)
    },
  })
  const submit = form.handleSubmit((v) => save.mutate(v))
  const available = (categories.data?.data ?? []).filter((c) => !usedCategoryIds.includes(c.id))

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={line ? `Edit budget: ${line.category_name}` : 'Add budget line'}
      description={`Amounts are in ${currency}.`}
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} loading={save.isPending}>
            Save budget
          </Button>
        </>
      }
    >
      <form onSubmit={submit} className="space-y-4" noValidate>
        {save.error instanceof ApiError && !save.error.fields.amount && <FormError error={save.error} />}
        {!line && (
          <Field label="Category" hint="A general line covers every category without its own line.">
            {(p) => (
              <Select {...p} {...form.register('category_id')}>
                {!usedCategoryIds.includes(null) && <option value="">General (all other categories)</option>}
                {available.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        )}
        <Field label={`Amount (${currency})`} error={form.formState.errors.amount?.message ?? fieldError(save.error, 'amount')}>
          {(p) => <Input {...p} inputMode="decimal" placeholder="0.00" {...form.register('amount')} />}
        </Field>
        <Field label="Notes">{(p) => <Textarea {...p} rows={2} {...form.register('notes')} />}</Field>
      </form>
    </Dialog>
  )
}
