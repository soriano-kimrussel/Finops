import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { FileText, Plus } from 'lucide-react'
import { Link } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { DateFilter, FilterBar, FilterSelect, InvoiceStatusBadge, Money, Pagination, SearchBox, SourceBadge } from '@/components/data'
import { Button, Card, EmptyState, ErrorState, PageHeader, Spinner, Table, Td, Th } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { useFilters } from '@/lib/hooks'
import type { InvoiceListItem, ListResponse } from '@/lib/models'

const LIMIT = 25
const statusOptions = [
  { value: 'draft', label: 'Draft' },
  { value: 'sent', label: 'Sent' },
  { value: 'partially_paid', label: 'Partially paid' },
  { value: 'paid', label: 'Paid' },
  { value: 'overdue', label: 'Overdue' },
  { value: 'outstanding', label: 'Outstanding' },
  { value: 'void', label: 'Void' },
]

export function InvoicesPage() {
  const { orgId, can, path } = useOrg()
  const { values, set } = useFilters(['q', 'status', 'from', 'to', 'customer_id', 'offset'] as const)
  const offset = Number(values.offset) || 0
  const query = useQuery({
    queryKey: orgKey(orgId, 'invoices', values),
    queryFn: () =>
      api<ListResponse<InvoiceListItem>>(
        `/orgs/${orgId}/invoices${qs({ q: values.q, status: values.status, from: values.from, to: values.to, customer_id: values.customer_id, limit: LIMIT, offset })}`,
      ),
    placeholderData: keepPreviousData,
  })
  const filtered = Boolean(values.q || values.status || values.from || values.to || values.customer_id)

  return (
    <>
      <PageHeader
        title="Invoices"
        description="Bill customers and track what has been collected"
        actions={
          can('invoices.manage') && (
            <Link to={path('invoices/new')}>
              <Button>
                <Plus className="size-4" aria-hidden /> New invoice
              </Button>
            </Link>
          )
        }
      />
      <Card>
        <FilterBar>
          <SearchBox value={values.q} onChange={(v) => set('q', v)} placeholder="Search number or customer" label="Search invoices" />
          <FilterSelect label="Status" value={values.status} onChange={(v) => set('status', v)} options={statusOptions} />
          <DateFilter label="Issued from" value={values.from} onChange={(v) => set('from', v)} />
          <DateFilter label="to" value={values.to} onChange={(v) => set('to', v)} />
        </FilterBar>
        {query.isPending ? (
          <Spinner />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        ) : query.data.data.length === 0 ? (
          <EmptyState icon={<FileText className="size-6" />} title={filtered ? 'No invoices match these filters' : 'No invoices yet'} description={filtered ? 'Try widening the date range or clearing the status filter.' : 'Create an invoice to bill a customer.'} />
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <Th>Number</Th>
                  <Th>Customer</Th>
                  <Th>Status</Th>
                  <Th>Issued</Th>
                  <Th>Due</Th>
                  <Th align="right">Total</Th>
                  <Th align="right">Balance</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {query.data.data.map((i) => (
                  <tr key={i.id} className="hover:bg-slate-50">
                    <Td>
                      <Link to={path(`invoices/${i.id}`)} className="font-medium text-slate-900 hover:text-brand-700">
                        {i.number}
                      </Link>{' '}
                      <SourceBadge source={i.source} />
                    </Td>
                    <Td>
                      <div>{i.customer_name}</div>
                      {i.project_name && <div className="text-xs text-slate-500">{i.project_name}</div>}
                    </Td>
                    <Td>
                      <InvoiceStatusBadge status={i.display_status ?? i.status} />
                    </Td>
                    <Td>{formatDate(i.issue_date)}</Td>
                    <Td className={i.is_overdue ? 'font-medium text-red-600' : ''}>{formatDate(i.due_date)}</Td>
                    <Td align="right">
                      <Money amount={i.total} currency={i.currency} />
                    </Td>
                    <Td align="right">
                      <Money amount={i.balance} currency={i.currency} className="font-medium text-slate-900" />
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
