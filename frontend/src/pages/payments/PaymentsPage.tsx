import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { CreditCard } from 'lucide-react'
import { Link } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { DateFilter, FilterBar, FilterSelect, Money, Pagination, SearchBox, SourceBadge } from '@/components/data'
import { Badge, Card, EmptyState, ErrorState, PageHeader, Spinner, Table, Td, Th } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { formatDate, humanize } from '@/lib/format'
import { useFilters } from '@/lib/hooks'
import type { ListResponse, Payment } from '@/lib/models'

const LIMIT = 25

export function PaymentsPage() {
  const { orgId, path } = useOrg()
  const { values, set } = useFilters(['q', 'method', 'status', 'from', 'to', 'offset'] as const)
  const offset = Number(values.offset) || 0
  const query = useQuery({
    queryKey: orgKey(orgId, 'payments', values),
    queryFn: () =>
      api<ListResponse<Payment>>(`/orgs/${orgId}/payments${qs({ q: values.q, method: values.method, status: values.status, from: values.from, to: values.to, limit: LIMIT, offset })}`),
    placeholderData: keepPreviousData,
  })

  return (
    <>
      <PageHeader title="Payments" description="Money received against invoices. Payments are reversed from the invoice page." />
      <Card>
        <FilterBar>
          <SearchBox value={values.q} onChange={(v) => set('q', v)} placeholder="Search invoice, customer or reference" label="Search payments" />
          <FilterSelect
            label="Method"
            value={values.method}
            onChange={(v) => set('method', v)}
            options={['bank_transfer', 'card', 'check', 'cash', 'other'].map((m) => ({ value: m, label: humanize(m) }))}
          />
          <FilterSelect
            label="Status"
            value={values.status}
            onChange={(v) => set('status', v)}
            options={[
              { value: 'completed', label: 'Received' },
              { value: 'reversed', label: 'Reversed' },
            ]}
          />
          <DateFilter label="Paid from" value={values.from} onChange={(v) => set('from', v)} />
          <DateFilter label="to" value={values.to} onChange={(v) => set('to', v)} />
        </FilterBar>
        {query.isPending ? (
          <Spinner />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        ) : query.data.data.length === 0 ? (
          <EmptyState icon={<CreditCard className="size-6" />} title="No payments found" description="Payments appear here once they are recorded against a sent invoice." />
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <Th>Date</Th>
                  <Th>Invoice</Th>
                  <Th>Customer</Th>
                  <Th>Method</Th>
                  <Th>Reference</Th>
                  <Th>Status</Th>
                  <Th align="right">Amount</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {query.data.data.map((p) => (
                  <tr key={p.id} className="hover:bg-slate-50">
                    <Td>{formatDate(p.paid_on)}</Td>
                    <Td>
                      <Link to={path(`invoices/${p.invoice_id}`)} className="font-medium text-slate-900 hover:text-brand-700">
                        {p.invoice_number}
                      </Link>{' '}
                      <SourceBadge source={p.source} />
                    </Td>
                    <Td>{p.customer_name}</Td>
                    <Td>{humanize(p.method)}</Td>
                    <Td className="text-slate-500">{p.reference ?? '—'}</Td>
                    <Td>{p.status === 'reversed' ? <Badge tone="red">Reversed</Badge> : <Badge tone="green">Received</Badge>}</Td>
                    <Td align="right">
                      <Money amount={p.amount} currency={p.currency} className={p.status === 'reversed' ? 'text-slate-400 line-through' : 'font-medium text-slate-900'} />
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <Pagination total={query.data.total} limit={LIMIT} offset={offset} onChange={(o) => set('offset', String(o))} />
          </>
        )}
      </Card>
    </>
  )
}
