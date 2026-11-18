import { QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import type { ReactElement } from 'react'
import { createMemoryRouter, RouterProvider, type RouteObject } from 'react-router'
import { makeQueryClient } from '@/App'
import { routes } from '@/app/router'
import type { Me, OrgContext, Role } from '@/lib/types'

export const server = setupServer()

export function renderApp(path: string, rs: RouteObject[] = routes) {
  const router = createMemoryRouter(rs, { initialEntries: [path] })
  const client = makeQueryClient()
  client.setDefaultOptions({ queries: { retry: false } })
  const utils = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...utils, router, client }
}

export function renderWithQuery(ui: ReactElement) {
  const client = makeQueryClient()
  client.setDefaultOptions({ queries: { retry: false } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

export const ORG_ID = '11111111-1111-4111-8111-111111111111'

export const meFixture: Me = {
  user: { id: 'u1', email: 'ada@example.com', name: 'Ada Lovelace' },
  memberships: [{ organization: { id: ORG_ID, name: 'Acme Ltd', base_currency: 'USD', timezone: 'UTC' }, role: 'owner' }],
}

const allPerms = [
  'org.manage', 'members.view', 'members.manage', 'customers.view', 'customers.manage', 'projects.view', 'projects.manage',
  'budgets.view', 'budgets.manage', 'invoices.view', 'invoices.manage', 'payments.record', 'expenses.view', 'expenses.submit',
  'expenses.approve', 'reports.view', 'integrations.view', 'integrations.manage', 'sync.run', 'reconciliation.view',
  'reconciliation.review', 'audit.view',
]

export function orgFixture(role: Role = 'owner', permissions = allPerms): OrgContext {
  return { organization: meFixture.memberships[0]!.organization, role, permissions }
}

export function mockSession(opts: { role?: Role; permissions?: string[] } = {}) {
  server.use(
    http.get('*/api/auth/me', () => HttpResponse.json(meFixture)),
    http.get(`*/api/orgs/${ORG_ID}`, () => HttpResponse.json(orgFixture(opts.role, opts.permissions))),
  )
}
