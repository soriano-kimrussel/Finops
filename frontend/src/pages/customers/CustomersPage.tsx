import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Plus, Users } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { FilterBar, FilterSelect, Money, Pagination, SearchBox, SourceBadge } from '@/components/data'
import { Badge, Button, Card, EmptyState, ErrorState, PageHeader, Spinner, Table, Td, Th } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { useFilters } from '@/lib/hooks'
import type { Customer, ListResponse } from '@/lib/models'
import { CustomerFormDialog } from './CustomerForm'

const LIMIT = 25

export function CustomersPage() {
  const { orgId, can, path } = useOrg()
  const navigate = useNavigate()
  const { values, set } = useFilters(['q', 'status', 'offset'] as const)
  const offset = Number(values.offset) || 0
  const [creating, setCreating] = useState(false)

  const query = useQuery({
    queryKey: orgKey(orgId, 'customers', values),
    queryFn: () => api<ListResponse<Customer>>(`/orgs/${orgId}/customers${qs({ q: values.q, status: values.status, limit: LIMIT, offset })}`),
    placeholderData: keepPreviousData,
  })

  return (
    <>
      <PageHeader
        title="Customers"
        description="Who you bill, and what they owe"
        actions={
          can('customers.manage') && (
            <Button onClick={() => setCreating(true)}>
              <Plus className="size-4" aria-hidden /> New customer
            </Button>
          )
        }
      />
      <Card>
        <FilterBar>
          <SearchBox value={values.q} onChange={(v) => set('q', v)} placeholder="Search name, email or contact" label="Search customers" />
          <FilterSelect
            label="Status"
            value={values.status}
            onChange={(v) => set('status', v)}
            options={[
              { value: 'active', label: 'Active' },
              { value: 'inactive', label: 'Inactive' },
            ]}
          />
        </FilterBar>
        {query.isPending ? (
          <Spinner />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        ) : query.data.data.length === 0 ? (
          <EmptyState
            icon={<Users className="size-6" />}
            title={values.q || values.status ? 'No customers match these filters' : 'No customers yet'}
            description={values.q || values.status ? 'Try a different search.' : 'Add your first customer to start invoicing.'}
            action={
              can('customers.manage') && !values.q && (
                <Button onClick={() => setCreating(true)}>
                  <Plus className="size-4" aria-hidden /> New customer
                </Button>
              )
            }
          />
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <Th>Customer</Th>
                  <Th>Contact</Th>
                  <Th>Status</Th>
                  <Th align="right">Invoices</Th>
                  <Th align="right">Outstanding</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {query.data.data.map((c) => (
                  <tr key={c.id} className="hover:bg-slate-50">
                    <Td>
                      <Link to={path(`customers/${c.id}`)} className="font-medium text-slate-900 hover:text-brand-700">
                        {c.name}
                      </Link>{' '}
                      <SourceBadge source={c.source} />
                    </Td>
                    <Td>
                      <div>{c.contact_name ?? '—'}</div>
                      <div className="text-xs text-slate-500">{c.email}</div>
                    </Td>
                    <Td>
                      <Badge tone={c.status === 'active' ? 'green' : 'gray'}>{c.status === 'active' ? 'Active' : 'Inactive'}</Badge>
                    </Td>
                    <Td align="right">{c.invoice_count ?? 0}</Td>
                    <Td align="right">
                      <Money amount={c.outstanding} currency={c.currency} className="font-medium text-slate-900" />
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <Pagination total={query.data.total} limit={LIMIT} offset={offset} onChange={(o) => set('offset', String(o))} />
          </>
        )}
      </Card>
      <CustomerFormDialog open={creating} onOpenChange={setCreating} onSaved={(c) => navigate(path(`customers/${c.id}`))} />
    </>
  )
}
