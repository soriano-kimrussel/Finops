import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { Link, useNavigate } from 'react-router'
import { z } from 'zod'
import { meKey, useMe } from '@/app/auth'
import { Button, Card, Field, FormError, Input, Select, fieldError } from '@/components/ui'
import { api } from '@/lib/api'
import type { Currency, Organization } from '@/lib/types'

const timezones = (() => {
  try {
    return Intl.supportedValuesOf('timeZone')
  } catch {
    return ['UTC', 'America/New_York', 'Europe/London']
  }
})()

const schema = z.object({
  name: z.string().trim().min(1, 'Enter the organization name').max(200),
  base_currency: z.string().length(3, 'Choose a currency'),
  timezone: z.string().min(1, 'Choose a time zone'),
})

type Values = z.infer<typeof schema>

export function CreateOrgPage() {
  const me = useMe()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const currencies = useQuery({ queryKey: ['currencies'], queryFn: () => api<{ data: Currency[] }>('/currencies'), staleTime: Infinity })
  const browserTz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  const form = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { name: '', base_currency: 'USD', timezone: browserTz } })

  const create = useMutation({
    mutationFn: (body: Values) => api<{ organization: Organization }>('/orgs', { method: 'POST', body }),
    onSuccess: async (res) => {
      await qc.invalidateQueries({ queryKey: meKey })
      navigate(`/o/${res.organization.id}/overview`)
    },
  })

  const hasOrgs = (me.data?.memberships.length ?? 0) > 0
  const errors = form.formState.errors

  return (
    <div className="flex min-h-full items-center justify-center bg-slate-50 px-4 py-12">
      <div className="w-full max-w-md">
        <h1 className="text-xl font-semibold tracking-tight text-slate-900">Create an organization</h1>
        <p className="mt-1 text-sm text-slate-500">An organization holds one business's customers, invoices, expenses and reports. You will be its owner.</p>
        <Card className="mt-6 p-6">
          <form onSubmit={form.handleSubmit((v) => create.mutate(v))} className="space-y-4" noValidate>
            <FormError error={create.error} />
            <Field label="Organization name" error={errors.name?.message ?? fieldError(create.error, 'name')}>
              {(p) => <Input {...p} placeholder="Northwind Consulting LLC" {...form.register('name')} />}
            </Field>
            <Field
              label="Base currency"
              hint="Dashboards and reports are shown in this currency. It cannot be changed later."
              error={errors.base_currency?.message ?? fieldError(create.error, 'base_currency')}
            >
              {(p) => (
                <Select {...p} {...form.register('base_currency')}>
                  {(currencies.data?.data ?? [{ code: 'USD', name: 'US Dollar', minor_units: 2 }]).map((c) => (
                    <option key={c.code} value={c.code}>
                      {c.code} — {c.name}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <Field label="Time zone" hint="Used to decide when invoices become overdue." error={errors.timezone?.message ?? fieldError(create.error, 'timezone')}>
              {(p) => (
                <Select {...p} {...form.register('timezone')}>
                  {timezones.map((tz) => (
                    <option key={tz} value={tz}>
                      {tz}
                    </option>
                  ))}
                </Select>
              )}
            </Field>
            <div className="flex items-center justify-end gap-2 pt-2">
              {hasOrgs && (
                <Link to="/" className="text-sm font-medium text-slate-600 hover:text-slate-900">
                  Cancel
                </Link>
              )}
              <Button type="submit" loading={create.isPending}>
                Create organization
              </Button>
            </div>
          </form>
        </Card>
      </div>
    </div>
  )
}
