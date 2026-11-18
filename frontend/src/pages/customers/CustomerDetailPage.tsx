import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, Pencil, Plus } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { DetailList, InvoiceStatusBadge, Money, ProjectStatusBadge, SourceBadge } from '@/components/data'
import { Badge, Button, Card, CardHeader, EmptyState, ErrorState, PageHeader, Spinner, StatCard, Table, Td, Th } from '@/components/ui'
import { api } from '@/lib/api'
import { formatDate, formatMoney, isZero } from '@/lib/format'
import type { CustomerDetail } from '@/lib/models'
import { CustomerFormDialog } from './CustomerForm'

export function CustomerDetailPage() {
  const { id = '' } = useParams()
  const { orgId, can, path } = useOrg()
  const [editing, setEditing] = useState(false)
  const query = useQuery({
    queryKey: orgKey(orgId, 'customers', 'detail', id),
    queryFn: () => api<CustomerDetail>(`/orgs/${orgId}/customers/${id}`),
  })

  if (query.isPending) return <Spinner />
  if (query.error) return <ErrorState error={query.error} onRetry={() => query.refetch()} />
  const { customer: c, summary, invoices, projects } = query.data
  const main = summary.find((s) => s.currency === c.currency) ?? { currency: c.currency, invoiced: '0', paid: '0', outstanding: '0', overdue: '0' }
  const address = [c.address_line1, c.address_line2, [c.city, c.region, c.postal_code].filter(Boolean).join(', '), c.country].filter(Boolean)

  return (
    <>
      <Link to={path('customers')} className="mb-3 inline-flex items-center gap-1 text-sm text-slate-500 hover:text-slate-700">
        <ArrowLeft className="size-4" aria-hidden /> Customers
      </Link>
      <PageHeader
        title={c.name}
        description={c.contact_name ? `Contact: ${c.contact_name}` : undefined}
        actions={
          <>
            <Badge tone={c.status === 'active' ? 'green' : 'gray'}>{c.status === 'active' ? 'Active' : 'Inactive'}</Badge>
            <SourceBadge source={c.source} />
            {can('customers.manage') && (
              <Button variant="secondary" onClick={() => setEditing(true)}>
                <Pencil className="size-4" aria-hidden /> Edit
              </Button>
            )}
            {can('invoices.manage') && c.status === 'active' && (
              <Link to={path(`invoices/new?customer_id=${c.id}`)}>
                <Button>
                  <Plus className="size-4" aria-hidden /> New invoice
                </Button>
              </Link>
            )}
          </>
        }
      />

      <div className="mb-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Invoiced" value={formatMoney(main.invoiced, c.currency)} />
        <StatCard label="Paid" value={formatMoney(main.paid, c.currency)} tone="positive" />
        <StatCard label="Outstanding" value={formatMoney(main.outstanding, c.currency)} />
        <StatCard label="Overdue" value={formatMoney(main.overdue, c.currency)} tone={isZero(main.overdue) ? 'default' : 'negative'} />
      </div>

      <div className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-1">
          <CardHeader title="Contact details" />
          <div className="p-5">
            <DetailList
              items={[
                { label: 'Email', value: c.email ? <a className="text-brand-700 hover:underline" href={`mailto:${c.email}`}>{c.email}</a> : '—' },
                { label: 'Phone', value: c.phone ?? '—' },
                { label: 'Billing currency', value: c.currency },
                { label: 'Customer since', value: formatDate(c.created_at) },
                { label: 'Address', value: address.length ? address.map((l) => <div key={l}>{l}</div>) : '—' },
                { label: 'Notes', value: c.notes ?? '—' },
              ]}
            />
          </div>
        </Card>

        <div className="space-y-6 lg:col-span-2">
          <Card>
            <CardHeader title="Recent invoices" />
            {invoices.length === 0 ? (
              <EmptyState title="No invoices yet" />
            ) : (
              <Table>
                <thead>
                  <tr>
                    <Th>Number</Th>
                    <Th>Status</Th>
                    <Th>Due</Th>
                    <Th align="right">Total</Th>
                    <Th align="right">Balance</Th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100">
                  {invoices.map((i) => (
                    <tr key={i.id} className="hover:bg-slate-50">
                      <Td>
                        <Link to={path(`invoices/${i.id}`)} className="font-medium text-slate-900 hover:text-brand-700">
                          {i.number}
                        </Link>
                      </Td>
                      <Td>
                        <InvoiceStatusBadge status={i.is_overdue ? 'overdue' : i.status} />
                      </Td>
                      <Td>{formatDate(i.due_date)}</Td>
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
            )}
          </Card>
          <Card>
            <CardHeader title="Projects" />
            {projects.length === 0 ? (
              <EmptyState title="No projects for this customer" />
            ) : (
              <ul className="divide-y divide-slate-100">
                {projects.map((p) => (
                  <li key={p.id} className="flex items-center justify-between px-5 py-3">
                    <div>
                      <Link to={path(`projects/${p.id}`)} className="text-sm font-medium text-slate-900 hover:text-brand-700">
                        {p.name}
                      </Link>
                      <p className="text-xs text-slate-500">
                        {p.code ? `${p.code} · ` : ''}
                        {formatDate(p.start_date)} – {p.end_date ? formatDate(p.end_date) : 'ongoing'}
                      </p>
                    </div>
                    <ProjectStatusBadge status={p.status} />
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </div>
      <CustomerFormDialog open={editing} onOpenChange={setEditing} customer={c} onSaved={() => query.refetch()} />
    </>
  )
}
