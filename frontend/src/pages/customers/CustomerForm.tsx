import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { orgKey, useOrg } from '@/app/org'
import { Dialog } from '@/components/Dialog'
import { Button, Field, FormError, Input, Select, Textarea, fieldError } from '@/components/ui'
import { api, ApiError } from '@/lib/api'
import type { Customer } from '@/lib/models'
import type { Currency } from '@/lib/types'

const optional = z.string().trim().max(200).optional().or(z.literal(''))

export const customerSchema = z.object({
  name: z.string().trim().min(1, 'Enter the customer name').max(200),
  contact_name: optional,
  email: z.string().trim().email('Enter a valid email address').optional().or(z.literal('')),
  phone: z.string().trim().max(50).optional().or(z.literal('')),
  address_line1: optional,
  address_line2: optional,
  city: optional,
  region: optional,
  postal_code: z.string().trim().max(20).optional().or(z.literal('')),
  country: optional,
  currency: z.string().length(3),
  status: z.enum(['active', 'inactive']),
  notes: z.string().max(2000).optional().or(z.literal('')),
})

export type CustomerValues = z.infer<typeof customerSchema>

function toValues(c: Customer | undefined, currency: string): CustomerValues {
  return {
    name: c?.name ?? '',
    contact_name: c?.contact_name ?? '',
    email: c?.email ?? '',
    phone: c?.phone ?? '',
    address_line1: c?.address_line1 ?? '',
    address_line2: c?.address_line2 ?? '',
    city: c?.city ?? '',
    region: c?.region ?? '',
    postal_code: c?.postal_code ?? '',
    country: c?.country ?? '',
    currency: c?.currency ?? currency,
    status: c?.status ?? 'active',
    notes: c?.notes ?? '',
  }
}

export function CustomerFormDialog({
  open,
  onOpenChange,
  customer,
  onSaved,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  customer?: Customer
  onSaved?: (c: Customer) => void
}) {
  const { orgId, organization } = useOrg()
  const qc = useQueryClient()
  const currencies = useQuery({ queryKey: ['currencies'], queryFn: () => api<{ data: Currency[] }>('/currencies'), staleTime: Infinity })
  const form = useForm<CustomerValues>({ resolver: zodResolver(customerSchema), defaultValues: toValues(customer, organization.base_currency) })

  useEffect(() => {
    if (open) form.reset(toValues(customer, organization.base_currency))
  }, [open, customer, organization.base_currency, form])

  const save = useMutation({
    mutationFn: (values: CustomerValues) =>
      customer
        ? api<{ customer: Customer }>(`/orgs/${orgId}/customers/${customer.id}`, { method: 'PATCH', body: values })
        : api<{ customer: Customer }>(`/orgs/${orgId}/customers`, { method: 'POST', body: values }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: orgKey(orgId, 'customers') })
      onOpenChange(false)
      onSaved?.(res.customer)
    },
  })

  const err = (f: keyof CustomerValues) => form.formState.errors[f]?.message ?? fieldError(save.error, f)
  const submit = form.handleSubmit((v) => save.mutate(v))

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={customer ? 'Edit customer' : 'New customer'}
      wide
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} loading={save.isPending}>
            {customer ? 'Save changes' : 'Create customer'}
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
        <Field label="Company name" error={err('name')} className="sm:col-span-2">
          {(p) => <Input {...p} {...form.register('name')} />}
        </Field>
        <Field label="Contact name" error={err('contact_name')}>
          {(p) => <Input {...p} {...form.register('contact_name')} />}
        </Field>
        <Field label="Email" error={err('email')}>
          {(p) => <Input {...p} type="email" {...form.register('email')} />}
        </Field>
        <Field label="Phone" error={err('phone')}>
          {(p) => <Input {...p} {...form.register('phone')} />}
        </Field>
        <Field label="Billing currency" error={err('currency')}>
          {(p) => (
            <Select {...p} {...form.register('currency')}>
              {(currencies.data?.data ?? [{ code: organization.base_currency, name: organization.base_currency, minor_units: 2 }]).map((c) => (
                <option key={c.code} value={c.code}>
                  {c.code} — {c.name}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Address line 1" error={err('address_line1')}>
          {(p) => <Input {...p} {...form.register('address_line1')} />}
        </Field>
        <Field label="Address line 2" error={err('address_line2')}>
          {(p) => <Input {...p} {...form.register('address_line2')} />}
        </Field>
        <Field label="City" error={err('city')}>
          {(p) => <Input {...p} {...form.register('city')} />}
        </Field>
        <Field label="State / region" error={err('region')}>
          {(p) => <Input {...p} {...form.register('region')} />}
        </Field>
        <Field label="Postal code" error={err('postal_code')}>
          {(p) => <Input {...p} {...form.register('postal_code')} />}
        </Field>
        <Field label="Country" error={err('country')}>
          {(p) => <Input {...p} {...form.register('country')} />}
        </Field>
        <Field label="Status" error={err('status')}>
          {(p) => (
            <Select {...p} {...form.register('status')}>
              <option value="active">Active</option>
              <option value="inactive">Inactive</option>
            </Select>
          )}
        </Field>
        <Field label="Notes" error={err('notes')} className="sm:col-span-2">
          {(p) => <Textarea {...p} rows={3} {...form.register('notes')} />}
        </Field>
      </form>
    </Dialog>
  )
}
