import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Ban, CreditCard, Pencil, Send, Undo2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { InvoiceStatusBadge, Money, SourceBadge } from '@/components/data'
import { ConfirmDialog } from '@/components/Dialog'
import { Alert, Badge, Button, Card, CardHeader, ErrorState, Field, FormError, PageHeader, Spinner, Table, Td, Textarea, Th } from '@/components/ui'
import { api } from '@/lib/api'
import { formatDate, formatDateTime, formatMoney, humanize, isZero } from '@/lib/format'
import type { Invoice, Payment } from '@/lib/models'
import { PaymentDialog } from './PaymentDialog'

export function InvoiceDetailPage() {
  const { id = '' } = useParams()
  const { orgId, can, path } = useOrg()
  const qc = useQueryClient()
  const key = orgKey(orgId, 'invoices', 'detail', id)
  const query = useQuery({ queryKey: key, queryFn: () => api<{ invoice: Invoice }>(`/orgs/${orgId}/invoices/${id}`) })
  const [paying, setPaying] = useState(false)
  const [sending, setSending] = useState(false)
  const [voiding, setVoiding] = useState(false)
  const [voidReason, setVoidReason] = useState('')
  const [reversing, setReversing] = useState<Payment | null>(null)
  const [reverseReason, setReverseReason] = useState('')

  const onDone = (res: { invoice: Invoice }) => {
    qc.setQueryData(key, { invoice: res.invoice })
    qc.invalidateQueries({ queryKey: orgKey(orgId) })
  }
  const send = useMutation({
    mutationFn: (inv: Invoice) => api<{ invoice: Invoice }>(`/orgs/${orgId}/invoices/${inv.id}/send`, { method: 'POST', body: { version: inv.version } }),
    onSuccess: (r) => {
      onDone(r)
      setSending(false)
    },
  })
  const voidInv = useMutation({
    mutationFn: (inv: Invoice) => api<{ invoice: Invoice }>(`/orgs/${orgId}/invoices/${inv.id}/void`, { method: 'POST', body: { reason: voidReason, version: inv.version } }),
    onSuccess: (r) => {
      onDone(r)
      setVoiding(false)
    },
  })
  const reverse = useMutation({
    mutationFn: (p: Payment) => api<{ invoice: Invoice }>(`/orgs/${orgId}/payments/${p.id}/reverse`, { method: 'POST', body: { reason: reverseReason } }),
    onSuccess: (r) => {
      onDone(r)
      setReversing(null)
    },
  })

  if (query.isPending) return <Spinner />
  if (query.error) return <ErrorState error={query.error} onRetry={() => query.refetch()} />
  const inv = query.data.invoice
  const cur = inv.currency
  const manage = can('invoices.manage')
  const canEdit = manage && (inv.status === 'draft' || (inv.status === 'sent' && isZero(inv.amount_paid)))
  const canPay = can('payments.record') && (inv.status === 'sent' || inv.status === 'partially_paid')
  const canVoid = manage && inv.status !== 'void' && isZero(inv.amount_paid)

  return (
    <>
      <Link to={path('invoices')} className="mb-3 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-700">
        <ArrowLeft className="size-4" aria-hidden /> Invoices
      </Link>
      <PageHeader
        title={`Invoice ${inv.number}`}
        description={`${inv.customer_name}${inv.project_name ? ` · ${inv.project_name}` : ''}`}
        actions={
          <>
            <InvoiceStatusBadge status={inv.display_status} />
            <SourceBadge source={inv.source} />
            {canEdit && (
              <Link to={path(`invoices/${inv.id}/edit`)}>
                <Button variant="secondary">
                  <Pencil className="size-4" aria-hidden /> Edit
                </Button>
              </Link>
            )}
            {manage && inv.status === 'draft' && (
              <Button variant="secondary" onClick={() => setSending(true)}>
                <Send className="size-4" aria-hidden /> Mark as sent
              </Button>
            )}
            {canVoid && (
              <Button variant="secondary" onClick={() => setVoiding(true)}>
                <Ban className="size-4" aria-hidden /> Void
              </Button>
            )}
            {canPay && (
              <Button onClick={() => setPaying(true)}>
                <CreditCard className="size-4" aria-hidden /> Record payment
              </Button>
            )}
          </>
        }
      />

      {inv.status === 'void' && (
        <div className="mb-4">
          <Alert tone="amber">
            Voided {formatDateTime(inv.voided_at)}: {inv.void_reason}
          </Alert>
        </div>
      )}
      {inv.is_overdue && (
        <div className="mb-4">
          <Alert>
            Overdue since {formatDate(inv.due_date)}. {formatMoney(inv.balance, cur)} is still outstanding.
          </Alert>
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-3">
        <div className="space-y-6 lg:col-span-2">
          <Card>
            <div className="grid gap-4 border-b border-slate-100 p-5 sm:grid-cols-4">
              <Info label="Issued" value={formatDate(inv.issue_date)} />
              <Info label="Due" value={formatDate(inv.due_date)} />
              <Info label="Currency" value={cur} />
              <Info label="Sent" value={inv.sent_at ? formatDate(inv.sent_at) : 'Not sent'} />
            </div>
            <Table>
              <thead>
                <tr>
                  <Th>Description</Th>
                  <Th align="right">Qty</Th>
                  <Th align="right">Unit price</Th>
                  <Th align="right">Amount</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {inv.items.map((it) => (
                  <tr key={it.id}>
                    <Td className="whitespace-normal text-slate-900">{it.description}</Td>
                    <Td align="right">{it.quantity}</Td>
                    <Td align="right">
                      <Money amount={it.unit_price} currency={cur} />
                    </Td>
                    <Td align="right">
                      <Money amount={it.line_total} currency={cur} />
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <dl className="ml-auto max-w-xs space-y-1.5 border-t border-slate-100 p-5 text-sm">
              <SumRow label="Subtotal" amount={inv.subtotal} currency={cur} />
              {inv.discount_type !== 'none' && (
                <SumRow label={inv.discount_type === 'percent' ? `Discount (${inv.discount_value}%)` : 'Discount'} amount={inv.discount_total} currency={cur} negative />
              )}
              <SumRow label={`Tax (${inv.tax_rate}%)`} amount={inv.tax_total} currency={cur} />
              <div className="border-t border-slate-200 pt-1.5">
                <SumRow label="Total" amount={inv.total} currency={cur} strong />
              </div>
              <SumRow label="Paid" amount={inv.amount_paid} currency={cur} negative />
              <div className="border-t border-slate-200 pt-1.5">
                <SumRow label="Balance due" amount={inv.balance} currency={cur} strong testId="balance-due" />
              </div>
            </dl>
            {inv.notes && <p className="border-t border-slate-100 p-5 text-sm whitespace-pre-line text-slate-600">{inv.notes}</p>}
          </Card>
        </div>

        <Card>
          <CardHeader title="Payment history" />
          {inv.payments.length === 0 ? (
            <p className="p-5 text-sm text-slate-500">No payments recorded.</p>
          ) : (
            <ul className="divide-y divide-slate-100">
              {inv.payments.map((p) => (
                <li key={p.id} className="space-y-1 px-5 py-3 text-sm">
                  <div className="flex items-center justify-between">
                    <Money amount={p.amount} currency={p.currency} className={`font-semibold ${p.status === 'reversed' ? 'text-slate-400 line-through' : 'text-slate-900'}`} />
                    {p.status === 'reversed' ? <Badge tone="red">Reversed</Badge> : <Badge tone="green">Received</Badge>}
                  </div>
                  <p className="text-xs text-slate-500">
                    {formatDate(p.paid_on)} · {humanize(p.method)}
                    {p.reference ? ` · ${p.reference}` : ''}
                  </p>
                  {p.recorded_by_name && <p className="text-xs text-slate-400">Recorded by {p.recorded_by_name}</p>}
                  {p.status === 'reversed' && <p className="text-xs text-red-600">Reversed: {p.reversal_reason}</p>}
                  {p.status === 'completed' && can('payments.record') && (
                    <button className="inline-flex items-center gap-1 text-xs font-medium text-slate-500 hover:text-red-600" onClick={() => setReversing(p)}>
                      <Undo2 className="size-3" aria-hidden /> Reverse
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>

      {paying && <PaymentDialog invoice={inv} open={paying} onOpenChange={setPaying} />}
      <ConfirmDialog
        open={sending}
        onOpenChange={setSending}
        title="Mark invoice as sent"
        description={`${inv.number} for ${formatMoney(inv.total, cur)} will become payable. You can still edit it until a payment is recorded.`}
        confirmLabel="Mark as sent"
        loading={send.isPending}
        onConfirm={() => send.mutate(inv)}
      >
        <FormError error={send.error} />
      </ConfirmDialog>
      <ConfirmDialog
        open={voiding}
        onOpenChange={setVoiding}
        title={`Void ${inv.number}`}
        description="Voiding is permanent. The invoice stays in your records but no longer counts as revenue or receivable."
        confirmLabel="Void invoice"
        tone="danger"
        loading={voidInv.isPending}
        onConfirm={() => voidInv.mutate(inv)}
      >
        <div className="space-y-3">
          <Field label="Reason">{(p) => <Textarea {...p} rows={2} value={voidReason} onChange={(e) => setVoidReason(e.target.value)} />}</Field>
          <FormError error={voidInv.error} />
        </div>
      </ConfirmDialog>
      <ConfirmDialog
        open={reversing !== null}
        onOpenChange={(o) => !o && setReversing(null)}
        title="Reverse payment"
        description={reversing ? `Reverse ${formatMoney(reversing.amount, reversing.currency)} received ${formatDate(reversing.paid_on)}? The invoice balance goes back up and a reversal entry is added to the ledger.` : undefined}
        confirmLabel="Reverse payment"
        tone="danger"
        loading={reverse.isPending}
        onConfirm={() => reversing && reverse.mutate(reversing)}
      >
        <div className="space-y-3">
          <Field label="Reason">{(p) => <Textarea {...p} rows={2} value={reverseReason} onChange={(e) => setReverseReason(e.target.value)} />}</Field>
          <FormError error={reverse.error} />
        </div>
      </ConfirmDialog>
    </>
  )
}

function Info({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs font-medium uppercase tracking-wide text-slate-500">{label}</p>
      <p className="mt-1 text-sm text-slate-900">{value}</p>
    </div>
  )
}

function SumRow({ label, amount, currency, strong, negative, testId }: { label: string; amount: string; currency: string; strong?: boolean; negative?: boolean; testId?: string }) {
  return (
    <div className={`flex justify-between ${strong ? 'font-semibold text-slate-900' : 'text-slate-600'}`}>
      <dt>{label}</dt>
      <dd className="tabular-nums" data-testid={testId}>
        {negative && !isZero(amount) ? '−' : ''}
        {formatMoney(amount, currency)}
      </dd>
    </div>
  )
}
