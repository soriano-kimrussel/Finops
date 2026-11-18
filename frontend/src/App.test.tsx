import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { meFixture, mockSession, ORG_ID, renderApp, server } from './test/utils'

describe('authentication flow', () => {
  it('redirects anonymous users to the login page', async () => {
    server.use(http.get('*/api/auth/me', () => HttpResponse.json({ error: { code: 'unauthorized', message: 'x' } }, { status: 401 })))
    const { router } = renderApp(`/o/${ORG_ID}/overview`)
    expect(await screen.findByRole('heading', { name: 'Sign in to FinOps HQ' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
  })

  it('validates the login form before calling the API', async () => {
    server.use(http.get('*/api/auth/me', () => HttpResponse.json({}, { status: 401 })))
    renderApp('/login')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('Enter your email')).toBeInTheDocument()
    expect(screen.getByText('Enter your password')).toBeInTheDocument()

    await user.type(screen.getByLabelText('Email'), 'not-an-email')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('Enter a valid email address')).toBeInTheDocument()
  })

  it('shows the server error for bad credentials and signs in on success', async () => {
    let loggedIn = false
    server.use(
      http.get('*/api/auth/me', () => (loggedIn ? HttpResponse.json(meFixture) : HttpResponse.json({}, { status: 401 }))),
      http.post('*/api/auth/login', async ({ request }) => {
        const body = (await request.json()) as { password: string }
        if (body.password !== 'correct horse battery staple') {
          return HttpResponse.json({ error: { code: 'invalid_credentials', message: 'email or password is incorrect' } }, { status: 401 })
        }
        loggedIn = true
        return HttpResponse.json({ user: meFixture.user })
      }),
      http.get(`*/api/orgs/${ORG_ID}`, () =>
        HttpResponse.json({ organization: meFixture.memberships[0]!.organization, role: 'owner', permissions: [] }),
      ),
    )
    const { router } = renderApp('/login')
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('Email'), 'ada@example.com')
    await user.type(screen.getByLabelText('Password'), 'wrong')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('email or password is incorrect')).toBeInTheDocument()

    await user.clear(screen.getByLabelText('Password'))
    await user.type(screen.getByLabelText('Password'), 'correct horse battery staple')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`/o/${ORG_ID}/overview`))
  })
})

describe('organization shell', () => {
  it('only shows navigation the role is allowed to use', async () => {
    mockSession({ role: 'viewer', permissions: ['customers.view', 'invoices.view', 'reports.view'] })
    renderApp(`/o/${ORG_ID}/overview`)
    const nav = await screen.findByRole('navigation', { name: 'Main' })
    expect(nav).toHaveTextContent('Customers')
    expect(nav).toHaveTextContent('Invoices')
    expect(nav).not.toHaveTextContent('Audit Logs')
    expect(nav).not.toHaveTextContent('Integrations')
  })

  it('sends non-members back home when the organization is not found', async () => {
    server.use(
      http.get('*/api/auth/me', () => HttpResponse.json({ ...meFixture, memberships: [] })),
      http.get('*/api/orgs/:id', () => HttpResponse.json({ error: { code: 'not_found', message: 'organization not found' } }, { status: 404 })),
      http.get('*/api/currencies', () => HttpResponse.json({ data: [] })),
    )
    const { router } = renderApp('/o/22222222-2222-4222-8222-222222222222/overview')
    await waitFor(() => expect(router.state.location.pathname).toBe('/orgs/new'))
  })
})
