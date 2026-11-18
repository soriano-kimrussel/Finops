import { useInfiniteQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, ScrollText } from 'lucide-react'
import { Fragment, useState } from 'react'
import { orgKey, useOrg } from '@/app/org'
import { DateFilter, FilterBar, FilterSelect } from '@/components/data'
import { Badge, Button, Card, EmptyState, ErrorState, PageHeader, Spinner, Table, Td, Th } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { formatDateTime, humanize } from '@/lib/format'
import { useFilters } from '@/lib/hooks'

type AuditLog = {
  id: string
  action: string
  entity_type: string
  entity_id: string | null
  actor_type: 'user' | 'system'
  actor_name: string | null
  actor_email: string | null
  changes: { before?: Record<string, unknown>; after?: Record<string, unknown>; [k: string]: unknown }
  request_id: string | null
  ip: string | null
  created_at: string
}

const entities = ['invoice', 'payment', 'expense', 'customer', 'project', 'budget', 'membership', 'organization', 'sync_connection', 'sync_job', 'reconciliation_record', 'reconciliation_run']

function fmt(v: unknown): string {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

function ChangeTable({ changes }: { changes: AuditLog['changes'] }) {
  const before = changes.before ?? {}
  const after = changes.after ?? {}
  const keys = Array.from(new Set([...Object.keys(before), ...Object.keys(after)]))
  const extra = Object.entries(changes).filter(([k]) => k !== 'before' && k !== 'after')
  return (
    <div className="space-y-3">
      {keys.length > 0 && (
        <table className="w-full text-xs">
          <thead>
            <tr className="text-left text-slate-500">
              <th className="py-1 pr-4 font-medium">Field</th>
              {changes.before && <th className="py-1 pr-4 font-medium">Before</th>}
              {changes.after && <th className="py-1 font-medium">After</th>}
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => {
              const changed = changes.before && changes.after && fmt(before[k]) !== fmt(after[k])
              return (
                <tr key={k} className={changed ? 'bg-amber-50' : ''}>
                  <td className="py-1 pr-4 font-mono text-slate-600">{k}</td>
                  {changes.before && <td className="max-w-xs break-all py-1 pr-4 text-slate-500">{fmt(before[k])}</td>}
                  {changes.after && <td className="max-w-xs break-all py-1 text-slate-900">{fmt(after[k])}</td>}
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
      {extra.map(([k, v]) => (
        <p key={k} className="text-xs text-slate-600">
          <span className="font-mono">{k}</span>: <span className="break-all">{fmt(v)}</span>
        </p>
      ))}
    </div>
  )
}

export function AuditLogsPage() {
  const { orgId } = useOrg()
  const { values, set } = useFilters(['entity_type', 'from', 'to'] as const)
  const [open, setOpen] = useState<string | null>(null)
  const query = useInfiniteQuery({
    queryKey: orgKey(orgId, 'audit', values),
    queryFn: ({ pageParam }) =>
      api<{ data: AuditLog[]; next_cursor: string | null }>(`/orgs/${orgId}/audit-logs${qs({ entity_type: values.entity_type, from: values.from, to: values.to, cursor: pageParam })}`),
    initialPageParam: '' as string,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const rows = query.data?.pages.flatMap((p) => p.data) ?? []

  return (
    <>
      <PageHeader title="Audit logs" description="An append-only record of every financial, permission and integration change. Entries cannot be edited or deleted." />
      <Card>
        <FilterBar>
          <FilterSelect label="Entity" value={values.entity_type} onChange={(v) => set('entity_type', v)} options={entities.map((e) => ({ value: e, label: humanize(e) }))} />
          <DateFilter label="From" value={values.from} onChange={(v) => set('from', v)} />
          <DateFilter label="to" value={values.to} onChange={(v) => set('to', v)} />
        </FilterBar>
        {query.isPending ? (
          <Spinner />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        ) : rows.length === 0 ? (
          <EmptyState icon={<ScrollText className="size-6" />} title="No audit entries match" />
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <Th />
                  <Th>When</Th>
                  <Th>Actor</Th>
                  <Th>Action</Th>
                  <Th>Entity</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {rows.map((a) => (
                  <Fragment key={a.id}>
                    <tr className="cursor-pointer hover:bg-slate-50" onClick={() => setOpen(open === a.id ? null : a.id)}>
                      <Td className="w-8">
                        <button aria-label={open === a.id ? 'Hide changes' : 'Show changes'} aria-expanded={open === a.id}>
                          {open === a.id ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
                        </button>
                      </Td>
                      <Td>{formatDateTime(a.created_at)}</Td>
                      <Td>
                        {a.actor_type === 'system' ? (
                          <Badge>System</Badge>
                        ) : (
                          <>
                            <div className="font-medium text-slate-900">{a.actor_name}</div>
                            <div className="text-xs text-slate-500">{a.actor_email}</div>
                          </>
                        )}
                      </Td>
                      <Td className="font-mono text-xs text-slate-700">{a.action}</Td>
                      <Td>
                        {humanize(a.entity_type)} <span className="font-mono text-xs text-slate-400">{a.entity_id?.slice(0, 8)}</span>
                      </Td>
                    </tr>
                    {open === a.id && (
                      <tr>
                        <td colSpan={5} className="bg-slate-50 px-12 py-4">
                          <ChangeTable changes={a.changes} />
                          <p className="mt-3 text-xs text-slate-400">
                            Request {a.request_id ?? '—'} · IP {a.ip ?? '—'}
                          </p>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                ))}
              </tbody>
            </Table>
            {query.hasNextPage && (
              <div className="border-t border-slate-100 p-4 text-center">
                <Button variant="secondary" onClick={() => query.fetchNextPage()} loading={query.isFetchingNextPage}>
                  Load older entries
                </Button>
              </div>
            )}
          </>
        )}
      </Card>
    </>
  )
}
