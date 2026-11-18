import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import clsx from 'clsx'
import { ArrowLeftRight, Play } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { FilterBar, FilterSelect, Pagination } from '@/components/data'
import { Dialog } from '@/components/Dialog'
import { Alert, Badge, Button, Card, EmptyState, ErrorState, Field, FormError, PageHeader, Select, Spinner, Table, Td, Textarea, Th, fieldError, type BadgeTone } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { formatDate, formatDateTime, formatMoney, humanize } from '@/lib/format'
import { useFilters } from '@/lib/hooks'

export type ReconRecord = {
  id: string
  entity_type: 'invoice' | 'payment'
  match_status: 'matched' | 'amount_mismatch' | 'date_mismatch' | 'missing_internal' | 'missing_external'
  review_status: 'open' | 'reviewed' | 'not_required' | 'superseded'
  decision: string | null
  decision_note: string | null
  reviewed_by_name: string | null
  reviewed_at: string | null
  reference: string | null
  counterparty: string | null
  currency: string | null
  external_amount: string | null
  internal_amount: string | null
  difference: string | null
  external_date: string | null
  internal_date: string | null
  invoice_id: string | null
  payment_id: string | null
  allowed_decisions: string[]
  created_at: string
}

type ListResult = { data: ReconRecord[]; total: number; summary: Record<string, Record<string, number>>; last_run: string | null }

const statusLabels: Record<ReconRecord['match_status'], string> = {
  matched: 'Matched',
  amount_mismatch: 'Amount differs',
  date_mismatch: 'Date differs',
  missing_internal: 'Only in QuickBooks',
  missing_external: 'Only in FinOps HQ',
}
const statusTones: Record<ReconRecord['match_status'], BadgeTone> = {
  matched: 'green',
  amount_mismatch: 'red',
  date_mismatch: 'amber',
  missing_internal: 'purple',
  missing_external: 'blue',
}
const decisionLabels: Record<string, string> = {
  accepted: 'Accept difference',
  follow_up: 'Needs follow-up',
  ignored: 'Ignore',
  imported: 'Create invoice from QuickBooks',
}

export function MatchBadge({ status }: { status: ReconRecord['match_status'] }) {
  return <Badge tone={statusTones[status]}>{statusLabels[status]}</Badge>
}

const LIMIT = 25

export function ReconciliationPage() {
  const { orgId, can } = useOrg()
  const qc = useQueryClient()
  const { values, set } = useFilters(['review_status', 'match_status', 'entity_type', 'offset'] as const)
  const review = values.review_status || 'open'
  const offset = Number(values.offset) || 0
  const [selected, setSelected] = useState<string | null>(null)
  const query = useQuery({
    queryKey: orgKey(orgId, 'reconciliation', values),
    queryFn: () =>
      api<ListResult>(
        `/orgs/${orgId}/reconciliation${qs({ review_status: review === 'all' ? undefined : review, match_status: values.match_status, entity_type: values.entity_type, limit: LIMIT, offset })}`,
      ),
    placeholderData: keepPreviousData,
  })
  const run = useMutation({
    mutationFn: () => api<{ new_open: number }>(`/orgs/${orgId}/reconciliation/run`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: orgKey(orgId, 'reconciliation') }),
  })

  const summary = query.data?.summary ?? {}
  const openCount = (s: string) => summary[s]?.open ?? 0

  return (
    <>
      <PageHeader
        title="Reconciliation"
        description="Differences between QuickBooks and your books, and the decisions taken about them"
        actions={
          can('reconciliation.review') && (
            <Button onClick={() => run.mutate()} loading={run.isPending}>
              <Play className="size-4" aria-hidden /> Run reconciliation
            </Button>
          )
        }
      />
      {run.data && (
        <div className="mb-4">
          <Alert tone="blue">Reconciliation finished: {run.data.new_open} new items need review.</Alert>
        </div>
      )}
      <FormError error={run.error} />
      <div className="mb-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        {(Object.keys(statusLabels) as ReconRecord['match_status'][])
          .filter((s) => s !== 'matched')
          .map((s) => (
            <button
              key={s}
              onClick={() => set('match_status', values.match_status === s ? '' : s)}
              className={clsx('rounded-lg border bg-white p-4 text-left shadow-sm transition-colors', values.match_status === s ? 'border-brand-500 ring-2 ring-brand-500/20' : 'border-slate-200 hover:border-slate-300')}
            >
              <p className="text-xs font-medium text-slate-500">{statusLabels[s]}</p>
              <p className="mt-1 text-2xl font-semibold text-slate-900">{openCount(s)}</p>
              <p className="text-xs text-slate-400">open</p>
            </button>
          ))}
        <div className="rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
          <p className="text-xs font-medium text-slate-500">Matched</p>
          <p className="mt-1 text-2xl font-semibold text-emerald-700">{summary.matched?.not_required ?? 0}</p>
          <p className="text-xs text-slate-400">{query.data?.last_run ? `last run ${formatDateTime(query.data.last_run)}` : 'never run'}</p>
        </div>
      </div>

      <Card>
        <FilterBar>
          <Select aria-label="Review" className="w-full sm:w-44" value={review} onChange={(e) => set('review_status', e.target.value === 'open' ? '' : e.target.value)}>
            <option value="open">Needs review</option>
            <option value="all">All current</option>
            <option value="reviewed">Reviewed</option>
            <option value="not_required">Matched</option>
            <option value="superseded">Superseded</option>
          </Select>
          <FilterSelect
            label="Type"
            value={values.entity_type}
            onChange={(v) => set('entity_type', v)}
            options={[
              { value: 'invoice', label: 'Invoices' },
              { value: 'payment', label: 'Payments' },
            ]}
          />
        </FilterBar>
        {query.isPending ? (
          <Spinner />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        ) : query.data.data.length === 0 ? (
          <EmptyState
            icon={<ArrowLeftRight className="size-6" />}
            title={review === 'open' ? 'Nothing to review' : 'No records'}
            description="Sync QuickBooks from the Integrations page; reconciliation runs automatically after each sync."
          />
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <Th>Record</Th>
                  <Th>Difference</Th>
                  <Th align="right">QuickBooks</Th>
                  <Th align="right">FinOps HQ</Th>
                  <Th align="right">Delta</Th>
                  <Th>Review</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {query.data.data.map((r) => (
                  <tr key={r.id} className="cursor-pointer hover:bg-slate-50" onClick={() => setSelected(r.id)}>
                    <Td>
                      <button className="text-left font-medium text-slate-900 hover:text-brand-700" onClick={() => setSelected(r.id)}>
                        {humanize(r.entity_type)} {r.reference ?? ''}
                      </button>
                      <div className="text-xs text-slate-500">{r.counterparty ?? '—'}</div>
                    </Td>
                    <Td>
                      <MatchBadge status={r.match_status} />
                    </Td>
                    <Td align="right">
                      <div>{r.external_amount && r.currency ? formatMoney(r.external_amount, r.currency) : '—'}</div>
                      <div className="text-xs text-slate-500">{formatDate(r.external_date)}</div>
                    </Td>
                    <Td align="right">
                      <div>{r.internal_amount && r.currency ? formatMoney(r.internal_amount, r.currency) : '—'}</div>
                      <div className="text-xs text-slate-500">{formatDate(r.internal_date)}</div>
                    </Td>
                    <Td align="right" className={r.difference && r.difference !== '0.00' ? 'font-medium text-red-600' : ''}>
                      {r.difference && r.currency ? formatMoney(r.difference, r.currency) : '—'}
                    </Td>
                    <Td>
                      {r.decision ? <Badge tone="green">{decisionLabels[r.decision] ?? humanize(r.decision)}</Badge> : <Badge tone={r.review_status === 'open' ? 'amber' : 'gray'}>{humanize(r.review_status)}</Badge>}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <Pagination total={query.data.total} limit={LIMIT} offset={offset} onChange={(o) => set('offset', String(o))} />
          </>
        )}
      </Card>
      {selected && <RecordDialog id={selected} onClose={() => setSelected(null)} />}
    </>
  )
}

function RecordDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const { orgId, can, path } = useOrg()
  const qc = useQueryClient()
  const [decision, setDecision] = useState('')
  const [note, setNote] = useState('')
  const q = useQuery({
    queryKey: orgKey(orgId, 'reconciliation', 'detail', id),
    queryFn: () => api<{ record: ReconRecord; history: ReconRecord[]; external_payload: unknown }>(`/orgs/${orgId}/reconciliation/${id}`),
  })
  const review = useMutation({
    mutationFn: () => api<{ record: ReconRecord }>(`/orgs/${orgId}/reconciliation/${id}/review`, { method: 'POST', body: { decision, note } }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: orgKey(orgId) })
      onClose()
    },
  })
  const r = q.data?.record
  const canDecide = can('reconciliation.review') && (r?.allowed_decisions.length ?? 0) > 0

  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={r ? `${humanize(r.entity_type)} ${r.reference ?? ''}` : 'Reconciliation record'}
      wide
      footer={
        canDecide ? (
          <>
            <Button variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button onClick={() => review.mutate()} loading={review.isPending} disabled={!decision}>
              Record decision
            </Button>
          </>
        ) : undefined
      }
    >
      {q.isPending ? (
        <Spinner />
      ) : q.error ? (
        <ErrorState error={q.error} />
      ) : (
        r && (
          <div className="space-y-5 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <MatchBadge status={r.match_status} />
              {r.counterparty && <span className="text-slate-600">{r.counterparty}</span>}
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <Side title="QuickBooks" amount={r.external_amount} date={r.external_date} currency={r.currency} highlight={r.match_status === 'amount_mismatch'} dateHighlight={r.match_status === 'date_mismatch'} />
              <Side title="FinOps HQ" amount={r.internal_amount} date={r.internal_date} currency={r.currency} highlight={r.match_status === 'amount_mismatch'} dateHighlight={r.match_status === 'date_mismatch'}>
                {r.invoice_id && (
                  <Link to={path(`invoices/${r.invoice_id}`)} className="text-xs font-medium text-brand-700 hover:underline">
                    Open invoice
                  </Link>
                )}
              </Side>
            </div>
            {r.difference && r.currency && (
              <p className="text-slate-700">
                Difference (QuickBooks − FinOps HQ): <strong className="tabular-nums">{formatMoney(r.difference, r.currency)}</strong>
              </p>
            )}

            {canDecide && (
              <fieldset className="space-y-3 rounded-md border border-slate-200 p-4">
                <legend className="px-1 text-sm font-semibold text-slate-900">Decision</legend>
                <div className="space-y-2">
                  {r.allowed_decisions.map((d) => (
                    <label key={d} className="flex items-center gap-2">
                      <input type="radio" name="decision" value={d} checked={decision === d} onChange={() => setDecision(d)} className="size-4 accent-brand-600" />
                      <span>{decisionLabels[d] ?? humanize(d)}</span>
                    </label>
                  ))}
                </div>
                <Field label={decision === 'imported' ? 'Note (optional)' : 'Note'} hint="Recorded in the audit trail" error={fieldError(review.error, 'note')}>
                  {(p) => <Textarea {...p} rows={2} value={note} onChange={(e) => setNote(e.target.value)} />}
                </Field>
                {review.error && !fieldError(review.error, 'note') && <FormError error={review.error} />}
              </fieldset>
            )}

            <div>
              <h3 className="mb-2 font-semibold text-slate-900">History</h3>
              <ol className="space-y-2 border-l border-slate-200 pl-4">
                {q.data.history.map((h) => (
                  <li key={h.id} className={clsx('relative', h.id === r.id && 'font-medium')}>
                    <span className="absolute -left-[21px] top-1.5 size-2 rounded-full bg-slate-300" />
                    <span className="text-slate-500">{formatDateTime(h.created_at)}</span> · {statusLabels[h.match_status]} · {humanize(h.review_status)}
                    {h.decision && (
                      <div className="text-slate-600">
                        {decisionLabels[h.decision] ?? h.decision} by {h.reviewed_by_name ?? 'someone'}: “{h.decision_note}”
                      </div>
                    )}
                  </li>
                ))}
              </ol>
            </div>
          </div>
        )
      )}
    </Dialog>
  )
}

function Side({
  title,
  amount,
  date,
  currency,
  highlight,
  dateHighlight,
  children,
}: {
  title: string
  amount: string | null
  date: string | null
  currency: string | null
  highlight: boolean
  dateHighlight: boolean
  children?: React.ReactNode
}) {
  return (
    <div className="rounded-md border border-slate-200 p-4">
      <p className="text-xs font-medium uppercase tracking-wide text-slate-500">{title}</p>
      {amount === null ? (
        <p className="mt-2 text-slate-400">No matching record</p>
      ) : (
        <>
          <p className={clsx('mt-2 text-lg font-semibold tabular-nums', highlight ? 'text-red-600' : 'text-slate-900')}>{currency ? formatMoney(amount, currency) : amount}</p>
          <p className={clsx('text-sm', dateHighlight ? 'font-medium text-amber-700' : 'text-slate-500')}>{formatDate(date)}</p>
        </>
      )}
      {children}
    </div>
  )
}
