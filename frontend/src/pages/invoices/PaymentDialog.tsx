import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { orgKey, useOrg } from '@/app/org'
import { Dialog } from '@/components/Dialog'
import { Alert, Button, Field, FormError, Input, Select, fieldError } from '@/components/ui'
import { api, ApiError, newIdempotencyKey } from '@/lib/api'
import { formatMoney, todayISO } from '@/lib/format'
import type { Invoice } from '@/lib/models'

export const paymentSchema = z.object({
  amount: z.string().trim().regex(/^\d{1,15}(\.\d{1,4})?$/, 'Enter an amount such as 250.00'),
  paid_on: z.string().min(1, 'Choose the payment date'),
  method: z.enum(['bank_transfer', 'card', 'cash', 'check', 'other']),
  reference: z.string().max(100),
})

type Values = z.infer<typeof paymentSchema>

export function PaymentDialog({ invoice, open, onOpenChange }: { invoice: Invoice; open: boolean; onOpenChange: (o: boolean) => void }) {
  const { orgId } = useOrg()
  const qc = useQueryClient()
  const [confirming, setConfirming] = useState<Values | null>(null)
  // One key per dialog session: retries and double-clicks resend the same key, so the server
  // records the payment at most once.
  // The parent mounts this dialog only while it is open, so each opening starts fresh.
  const [key] = useState(newIdempotencyKey)
  const form = useForm<Values>({ resolver: zodResolver(paymentSchema), defaultValues: { amount: invoice.balance, paid_on: todayISO(), method: 'bank_transfer', reference: '' } })

  const record = useMutation({
    mutationFn: (v: Values) =>
      api<{ invoice: Invoice }>(`/orgs/${orgId}/invoices/${invoice.id}/payments`, {
        method: 'POST',
        headers: { 'Idempotency-Key': key },
        body: { ...v, reference: v.reference || null },
      }),
    onSuccess: (res) => {
      qc.setQueryData(orgKey(orgId, 'invoices', 'detail', invoice.id), { invoice: res.invoice })
      qc.invalidateQueries({ queryKey: orgKey(orgId) })
      onOpenChange(false)
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 422) setConfirming(null)
    },
  })

  const errors = form.formState.errors
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={confirming ? 'Confirm payment' : `Record payment for ${invoice.number}`}
      description={confirming ? undefined : `Outstanding balance: ${formatMoney(invoice.balance, invoice.currency)}`}
      footer={
        confirming ? (
          <>
            <Button variant="secondary" onClick={() => setConfirming(null)} disabled={record.isPending}>
              Back
            </Button>
            <Button onClick={() => record.mutate(confirming)} loading={record.isPending}>
              Confirm payment
            </Button>
          </>
        ) : (
          <>
            <Button variant="secondary" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button onClick={form.handleSubmit((v) => setConfirming(v))}>Review payment</Button>
          </>
        )
      }
    >
      {confirming ? (
        <div className="space-y-3 text-sm text-slate-700">
          <p>
            Record <strong className="tabular-nums">{formatMoney(confirming.amount, invoice.currency)}</strong> received from <strong>{invoice.customer_name}</strong> on{' '}
            {confirming.paid_on} against <strong>{invoice.number}</strong>?
          </p>
          <Alert tone="blue">Recorded payments cannot be edited. A mistake is corrected by reversing the payment, which keeps both entries in the history.</Alert>
          <FormError error={record.error} />
        </div>
      ) : (
        <form className="grid gap-4 sm:grid-cols-2" noValidate onSubmit={form.handleSubmit((v) => setConfirming(v))}>
          {record.error && !fieldError(record.error, 'amount') && (
            <div className="sm:col-span-2">
              <FormError error={record.error} />
            </div>
          )}
          <Field label={`Amount (${invoice.currency})`} error={errors.amount?.message ?? fieldError(record.error, 'amount')}>
            {(p) => <Input {...p} inputMode="decimal" {...form.register('amount')} />}
          </Field>
          <Field label="Payment date" error={errors.paid_on?.message ?? fieldError(record.error, 'paid_on')}>
            {(p) => <Input {...p} type="date" max={todayISO()} {...form.register('paid_on')} />}
          </Field>
          <Field label="Method">
            {(p) => (
              <Select {...p} {...form.register('method')}>
                <option value="bank_transfer">Bank transfer</option>
                <option value="card">Card</option>
                <option value="check">Check</option>
                <option value="cash">Cash</option>
                <option value="other">Other</option>
              </Select>
            )}
          </Field>
          <Field label="Reference" hint="Bank reference or check number" error={fieldError(record.error, 'reference')}>
            {(p) => <Input {...p} {...form.register('reference')} />}
          </Field>
        </form>
      )}
    </Dialog>
  )
}
