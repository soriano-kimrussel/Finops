import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FlaskConical, Link2, Plug, RefreshCw, RotateCcw, Unplug } from 'lucide-react'
import { useState } from 'react'
import { useSearchParams } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { ConfirmDialog, Dialog } from '@/components/Dialog'
import { Alert, Badge, Button, Card, CardHeader, EmptyState, ErrorState, FormError, PageHeader, Select, Spinner, Table, Td, Th, type BadgeTone } from '@/components/ui'
import { api, newIdempotencyKey } from '@/lib/api'
import { formatDate, formatDateTime, formatMoney, humanize } from '@/lib/format'

type EntityStats = { fetched: number; created: number; updated: number; unchanged: number; rejected: number; linked?: number; imported?: number }
export type SyncJob = {
  id: string
  kind: string
  trigger: string
  status: 'queued' | 'running' | 'retrying' | 'succeeded' | 'failed'
  mode?: string
  attempts: number
  max_attempts: number
  run_at: string
  started_at: string | null
  finished_at: string | null
  last_error: string | null
  stats: { customers?: EntityStats; invoices?: EntityStats; payments?: EntityStats; pages?: number; rejected_examples?: string[]; reconciliation?: { new_open: number } }
  retry_of: string | null
  requested_by_name?: string | null
  created_at: string
}
type Connection = {
  id: string
  mode: 'live' | 'simulated'
  status: 'connected' | 'needs_reauth' | 'error' | 'disconnected'
  realm_id: string | null
  company_name: string | null
  connected_at: string | null
  last_synced_at: string | null
  last_error: string | null
}
type Status = { available_mode: 'live' | 'simulated'; simulated: boolean; connection: Connection | null; recent_jobs: SyncJob[] }
type ExternalRecord = {
  id: string
  entity_type: string
  external_id: string
  doc_number: string | null
  display_name: string | null
  txn_date: string | null
  amount: string | null
  balance: string | null
  currency: string | null
  simulated: boolean
  updated_at: string
}

const jobTones: Record<SyncJob['status'], BadgeTone> = { queued: 'gray', running: 'blue', retrying: 'amber', succeeded: 'green', failed: 'red' }

export function JobStatusBadge({ status }: { status: SyncJob['status'] }) {
  return <Badge tone={jobTones[status]}>{humanize(status)}</Badge>
}

function statsLine(j: SyncJob) {
  const parts: string[] = []
  for (const [label, s] of [
    ['customers', j.stats.customers],
    ['invoices', j.stats.invoices],
    ['payments', j.stats.payments],
  ] as const) {
    if (s) parts.push(`${s.fetched} ${label} (${s.created} new, ${s.updated} changed${s.rejected ? `, ${s.rejected} rejected` : ''})`)
  }
  return parts.join(' · ')
}

export function IntegrationsPage() {
  const { orgId, can } = useOrg()
  const qc = useQueryClient()
  const [params, setParams] = useSearchParams()
  const [kind, setKind] = useState('full')
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)
  const [detail, setDetail] = useState<string | null>(null)
  const [entity, setEntity] = useState('invoice')
  const key = orgKey(orgId, 'integrations')

  const status = useQuery({
    queryKey: key,
    queryFn: () => api<Status>(`/orgs/${orgId}/integrations/quickbooks`),
    refetchInterval: (q) => (q.state.data?.recent_jobs.some((j) => j.status === 'queued' || j.status === 'running') ? 1500 : false),
  })
  const records = useQuery({
    queryKey: orgKey(orgId, 'external-records', entity),
    queryFn: () => api<{ data: ExternalRecord[] }>(`/orgs/${orgId}/external-records?entity_type=${entity}&limit=100`),
    enabled: Boolean(status.data?.connection),
  })
  const refresh = () => {
    qc.invalidateQueries({ queryKey: key })
    qc.invalidateQueries({ queryKey: orgKey(orgId, 'external-records') })
    qc.invalidateQueries({ queryKey: orgKey(orgId, 'reconciliation') })
    qc.invalidateQueries({ queryKey: orgKey(orgId, 'dashboard') })
  }

  const connect = useMutation({
    mutationFn: () => api<{ authorize_url?: string }>(`/orgs/${orgId}/integrations/quickbooks/connect`, { method: 'POST' }),
    onSuccess: (res) => {
      if (res.authorize_url) window.location.assign(res.authorize_url)
      else refresh()
    },
  })
  const disconnect = useMutation({
    mutationFn: () => api(`/orgs/${orgId}/integrations/quickbooks/disconnect`, { method: 'POST' }),
    onSuccess: () => {
      setConfirmDisconnect(false)
      refresh()
    },
  })
  const sync = useMutation({
    mutationFn: () => api<{ job: SyncJob }>(`/orgs/${orgId}/sync-jobs`, { method: 'POST', body: { kind }, headers: { 'Idempotency-Key': newIdempotencyKey() } }),
    onSuccess: refresh,
  })
  const retry = useMutation({
    mutationFn: (id: string) => api(`/orgs/${orgId}/sync-jobs/${id}/retry`, { method: 'POST' }),
    onSuccess: refresh,
  })

  if (status.isPending) return <Spinner />
  if (status.error) return <ErrorState error={status.error} onRetry={() => status.refetch()} />
  const { connection: c, recent_jobs: jobs } = status.data
  const connected = c?.status === 'connected'
  const busy = jobs.some((j) => j.status === 'queued' || j.status === 'running')
  const manage = can('integrations.manage')

  return (
    <>
      <PageHeader title="Integrations" description="Import customers, invoices and payments from QuickBooks Online" />
      {params.get('connected') && (
        <div className="mb-4">
          <Alert tone="green">QuickBooks connected.</Alert>
        </div>
      )}
      {params.get('error') && (
        <div className="mb-4">
          <Alert>
            QuickBooks connection failed: {params.get('error')}{' '}
            <button className="underline" onClick={() => setParams({}, { replace: true })}>
              Dismiss
            </button>
          </Alert>
        </div>
      )}

      <Card className="mb-6">
        <div className="flex flex-wrap items-start justify-between gap-4 p-5">
          <div className="flex gap-4">
            <div className="flex size-12 items-center justify-center rounded-lg bg-emerald-50 text-emerald-700">
              <Plug className="size-6" aria-hidden />
            </div>
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-base font-semibold text-slate-900">QuickBooks Online</h2>
                {c && c.status !== 'disconnected' ? (
                  <Badge tone={connected ? 'green' : 'red'}>{humanize(c.status)}</Badge>
                ) : (
                  <Badge>Not connected</Badge>
                )}
                {(c?.mode ?? status.data.available_mode) === 'simulated' ? (
                  <Badge tone="purple">
                    <FlaskConical className="mr-1 size-3" aria-hidden /> Simulated — sample data
                  </Badge>
                ) : (
                  <Badge tone="blue">Live API</Badge>
                )}
              </div>
              <p className="mt-1 text-sm text-slate-500">
                {c && c.status !== 'disconnected'
                  ? `${c.company_name ?? 'Company'}${c.realm_id ? ` · realm ${c.realm_id}` : ''} · last sync ${c.last_synced_at ? formatDateTime(c.last_synced_at) : 'never'}`
                  : 'Connect to pull customers, invoices and payments into FinOps HQ for reconciliation.'}
              </p>
              {c?.last_error && <p className="mt-1 text-sm text-red-600">{c.last_error}</p>}
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {connected && can('sync.run') && (
              <>
                <Select aria-label="What to sync" className="w-40" value={kind} onChange={(e) => setKind(e.target.value)}>
                  <option value="full">Everything</option>
                  <option value="customers">Customers</option>
                  <option value="invoices">Invoices</option>
                  <option value="payments">Payments</option>
                </Select>
                <Button onClick={() => sync.mutate()} loading={sync.isPending} disabled={busy}>
                  <RefreshCw className={`size-4 ${busy ? 'animate-spin' : ''}`} aria-hidden /> {busy ? 'Syncing…' : 'Sync now'}
                </Button>
              </>
            )}
            {manage && (!c || c.status !== 'connected') && (
              <Button onClick={() => connect.mutate()} loading={connect.isPending}>
                <Link2 className="size-4" aria-hidden /> {c?.status === 'needs_reauth' ? 'Reconnect' : 'Connect QuickBooks'}
              </Button>
            )}
            {manage && c && c.status !== 'disconnected' && (
              <Button variant="secondary" onClick={() => setConfirmDisconnect(true)}>
                <Unplug className="size-4" aria-hidden /> Disconnect
              </Button>
            )}
          </div>
        </div>
        {status.data.simulated && (
          <div className="border-t border-slate-100 px-5 py-3">
            <Alert tone="amber">
              This server runs the <strong>simulated</strong> QuickBooks adapter: no data is exchanged with Intuit. It returns QuickBooks-shaped records built from this
              organization's own books with deliberate differences, so imports and reconciliation can be tried out. Set QUICKBOOKS_MODE=live with sandbox credentials
              to use the real API.
            </Alert>
          </div>
        )}
        <div className="px-5 pb-4">
          <FormError error={connect.error ?? sync.error ?? retry.error} />
        </div>
      </Card>

      <Card className="mb-6">
        <CardHeader title="Synchronization history" description="Jobs run in the background and retry transient failures with exponential backoff." />
        {jobs.length === 0 ? (
          <EmptyState icon={<RefreshCw className="size-6" />} title="No sync jobs yet" />
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>Started</Th>
                <Th>Scope</Th>
                <Th>Status</Th>
                <Th>Attempts</Th>
                <Th>Result</Th>
                <Th />
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {jobs.map((j) => (
                <tr key={j.id} className="hover:bg-slate-50">
                  <Td>
                    <button className="text-left font-medium text-slate-900 hover:text-brand-700" onClick={() => setDetail(j.id)}>
                      {formatDateTime(j.started_at ?? j.created_at)}
                    </button>
                    <div className="text-xs text-slate-500">
                      {humanize(j.trigger)}
                      {j.requested_by_name ? ` by ${j.requested_by_name}` : ''}
                    </div>
                  </Td>
                  <Td>{humanize(j.kind)}</Td>
                  <Td>
                    <JobStatusBadge status={j.status} />
                  </Td>
                  <Td>
                    {j.attempts}/{j.max_attempts}
                  </Td>
                  <Td className="max-w-md whitespace-normal text-xs text-slate-600">
                    {j.last_error && j.status !== 'succeeded' ? (
                      <span className="text-red-600">{j.last_error}</span>
                    ) : (
                      statsLine(j) || '—'
                    )}
                    {j.status === 'retrying' && <div className="text-amber-700">Next attempt {formatDateTime(j.run_at)}</div>}
                  </Td>
                  <Td align="right">
                    {j.status === 'failed' && can('sync.run') && (
                      <Button size="sm" variant="secondary" onClick={() => retry.mutate(j.id)} loading={retry.isPending && retry.variables === j.id}>
                        <RotateCcw className="size-3.5" aria-hidden /> Retry
                      </Button>
                    )}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {c && (
        <Card>
          <CardHeader
            title="Imported records"
            description="Staged exactly as received. Invoices and payments are compared on the Reconciliation page; they never overwrite your books."
            action={
              <Select aria-label="Record type" className="w-36" value={entity} onChange={(e) => setEntity(e.target.value)}>
                <option value="invoice">Invoices</option>
                <option value="payment">Payments</option>
                <option value="customer">Customers</option>
              </Select>
            }
          />
          {records.isPending ? (
            <Spinner />
          ) : !records.data?.data.length ? (
            <EmptyState title="Nothing imported yet" description="Run a sync to fetch records." />
          ) : (
            <Table>
              <thead>
                <tr>
                  <Th>QuickBooks ID</Th>
                  <Th>{entity === 'customer' ? 'Name' : 'Reference'}</Th>
                  <Th>Date</Th>
                  <Th align="right">Amount</Th>
                  <Th align="right">Balance</Th>
                  <Th>Source</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {records.data.data.map((r) => (
                  <tr key={r.id}>
                    <Td className="font-mono text-xs">{r.external_id}</Td>
                    <Td>{entity === 'customer' ? r.display_name : (r.doc_number ?? '—')}</Td>
                    <Td>{formatDate(r.txn_date)}</Td>
                    <Td align="right">{r.amount && r.currency ? formatMoney(r.amount, r.currency) : '—'}</Td>
                    <Td align="right">{r.balance && r.currency ? formatMoney(r.balance, r.currency) : '—'}</Td>
                    <Td>{r.simulated ? <Badge tone="purple">Simulated</Badge> : <Badge tone="blue">QuickBooks</Badge>}</Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>
      )}

      <ConfirmDialog
        open={confirmDisconnect}
        onOpenChange={setConfirmDisconnect}
        title="Disconnect QuickBooks"
        description="Stored tokens are deleted (and revoked with Intuit for live connections). Imported records and reconciliation history are kept."
        confirmLabel="Disconnect"
        tone="danger"
        loading={disconnect.isPending}
        onConfirm={() => disconnect.mutate()}
      >
        <FormError error={disconnect.error} />
      </ConfirmDialog>
      {detail && <JobDialog id={detail} onClose={() => setDetail(null)} />}
    </>
  )
}

function JobDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const { orgId } = useOrg()
  const q = useQuery({
    queryKey: orgKey(orgId, 'sync-job', id),
    queryFn: () =>
      api<{ job: SyncJob; attempts: { attempt: number; worker: string; started_at: string; finished_at: string; outcome: string; error: string | null }[] }>(
        `/orgs/${orgId}/sync-jobs/${id}`,
      ),
  })
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title="Sync job" wide>
      {q.isPending ? (
        <Spinner />
      ) : q.error ? (
        <ErrorState error={q.error} />
      ) : (
        <div className="space-y-4 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <JobStatusBadge status={q.data.job.status} />
            <span className="text-slate-600">
              {humanize(q.data.job.kind)} · {humanize(q.data.job.trigger)} · {q.data.job.attempts} of {q.data.job.max_attempts} attempts
            </span>
          </div>
          {q.data.job.stats.rejected_examples?.length ? (
            <Alert tone="amber">
              Rejected during validation:
              <ul className="mt-1 list-disc pl-5">
                {q.data.job.stats.rejected_examples.map((r) => (
                  <li key={r}>{r}</li>
                ))}
              </ul>
            </Alert>
          ) : null}
          <Table>
            <thead>
              <tr>
                <Th>Attempt</Th>
                <Th>Worker</Th>
                <Th>Finished</Th>
                <Th>Outcome</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100">
              {q.data.attempts.map((a) => (
                <tr key={a.attempt}>
                  <Td>#{a.attempt}</Td>
                  <Td className="font-mono text-xs">{a.worker}</Td>
                  <Td>{formatDateTime(a.finished_at)}</Td>
                  <Td className="whitespace-normal">
                    {humanize(a.outcome)}
                    {a.error && <div className="text-xs text-red-600">{a.error}</div>}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
    </Dialog>
  )
}
