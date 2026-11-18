import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { FolderKanban, Plus } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { orgKey, useOrg } from '@/app/org'
import { BudgetMeter, BudgetWarning, FilterBar, FilterSelect, Pagination, ProjectStatusBadge, SearchBox } from '@/components/data'
import { Button, Card, EmptyState, ErrorState, PageHeader, Spinner, Table, Td, Th } from '@/components/ui'
import { api, qs } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { useFilters } from '@/lib/hooks'
import type { ListResponse, Project } from '@/lib/models'
import { ProjectFormDialog } from './ProjectForms'

const LIMIT = 25
export const projectStatusOptions = [
  { value: 'planned', label: 'Planned' },
  { value: 'active', label: 'Active' },
  { value: 'on_hold', label: 'On hold' },
  { value: 'completed', label: 'Completed' },
  { value: 'cancelled', label: 'Cancelled' },
]

export function ProjectsPage() {
  const { orgId, can, path } = useOrg()
  const navigate = useNavigate()
  const { values, set } = useFilters(['q', 'status', 'offset'] as const)
  const offset = Number(values.offset) || 0
  const [creating, setCreating] = useState(false)
  const query = useQuery({
    queryKey: orgKey(orgId, 'projects', values),
    queryFn: () => api<ListResponse<Project>>(`/orgs/${orgId}/projects${qs({ q: values.q, status: values.status, limit: LIMIT, offset })}`),
    placeholderData: keepPreviousData,
  })

  return (
    <>
      <PageHeader
        title="Projects"
        description="Engagements, budgets and spending"
        actions={
          can('projects.manage') && (
            <Button onClick={() => setCreating(true)}>
              <Plus className="size-4" aria-hidden /> New project
            </Button>
          )
        }
      />
      <Card>
        <FilterBar>
          <SearchBox value={values.q} onChange={(v) => set('q', v)} placeholder="Search project, code or customer" label="Search projects" />
          <FilterSelect label="Status" value={values.status} onChange={(v) => set('status', v)} options={projectStatusOptions} />
        </FilterBar>
        {query.isPending ? (
          <Spinner />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => query.refetch()} />
        ) : query.data.data.length === 0 ? (
          <EmptyState
            icon={<FolderKanban className="size-6" />}
            title="No projects found"
            description={values.q || values.status ? 'Try different filters.' : 'Create a project to track budgets and spending for a customer.'}
          />
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <Th>Project</Th>
                  <Th>Customer</Th>
                  <Th>Status</Th>
                  <Th>Timeline</Th>
                  <Th>Budget used</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100">
                {query.data.data.map((p) => (
                  <tr key={p.id} className="hover:bg-slate-50">
                    <Td>
                      <Link to={path(`projects/${p.id}`)} className="font-medium text-slate-900 hover:text-brand-700">
                        {p.name}
                      </Link>
                      {p.code && <div className="text-xs text-slate-500">{p.code}</div>}
                    </Td>
                    <Td>{p.customer_name}</Td>
                    <Td>
                      <div className="flex flex-wrap gap-1">
                        <ProjectStatusBadge status={p.status} />
                        <BudgetWarning summary={p.budget} />
                      </div>
                    </Td>
                    <Td className="text-xs text-slate-500">
                      {formatDate(p.start_date)} – {p.end_date ? formatDate(p.end_date) : 'ongoing'}
                    </Td>
                    <Td>
                      <BudgetMeter summary={p.budget} currency={p.currency} compact />
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <Pagination total={query.data.total} limit={LIMIT} offset={offset} onChange={(o) => set('offset', String(o))} />
          </>
        )}
      </Card>
      <ProjectFormDialog open={creating} onOpenChange={setCreating} onSaved={(p) => navigate(path(`projects/${p.id}`))} />
    </>
  )
}
