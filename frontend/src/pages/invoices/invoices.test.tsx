import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Invoice } from '@/lib/models'
import { mockSession, ORG_ID, renderApp, server } from '@/test/utils'

const customer = { id: 'c1', name: 'Globex Corporation', currency: 'USD', status: 'active', source: 'internal' }

function invoiceFixture(overrides: Partial<Invoice> = {}): Invoice {
  return {
    id: 'inv1',
    number: 'INV-000042',
    status: 'partially_paid',
    display_status: 'partially_paid',
    is_overdue: false,
    customer_id: 'c1',
    customer_name: 'Globex Corporation',
    project_id: null,
    project_name: null,
    currency: 'USD',
    issue_date: '2026-10-01',
    due_date: '2026-10-31',
    discount_type: 'none',
    discount_value: '0',
    tax_rate: '0',
    subtotal: '1000.00',
    discount_total: '0.00',
    tax_total: '0.00',
    total: '1000.00',
    amount_paid: '400.00',
    balance: '600.00',
    notes: null,
    sent_at: '2026-10-01T10:00:00Z',
    voided_at: null,
    void_reason: null,
    source: 'internal',
    external_id: null,
    version: 3,
    created_at: '2026-10-01T10:00:00Z',
    updated_at: '2026-10-01T10:00:00Z',
    items: [{ id: 'it1', position: 1, description: 'Consulting', quantity: '1', unit_price: '1000.00', line_total: '1000.00' }],
    payments: [],
    ...overrides,
  }
}

describe('invoice editor', () => {
  it('shows totals calculated by the server, not the browser', async () => {
    mockSession()
    const previews: unknown[] = []
    server.use(
      http.get('*/api/orgs/:org/customers', () => HttpResponse.json({ data: [customer], total: 1 })),
      http.get('*/api/orgs/:org/projects', () => HttpResponse.json({ data: [], total: 0 })),
      http.post('*/api/orgs/:org/invoices/preview', async ({ request }) => {
        previews.push(await request.json())
        return HttpResponse.json({
          currency: 'USD',
          line_totals: ['59.97'],
          subtotal: '59.97',
          discount_total: '0.00',
          taxable_amount: '59.97',
          tax_total: '4.95',
          total: '64.92',
        })
      }),
    )
    renderApp(`/o/${ORG_ID}/invoices/new?customer_id=c1`)
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('Description'), 'Licenses')
    const qty = screen.getByLabelText('Qty')
    await user.clear(qty)
    await user.type(qty, '3')
    await user.type(screen.getByLabelText('Unit price'), '19.99')
    await user.type(screen.getByLabelText('Tax rate (%)'), '8.25')

    await waitFor(() => expect(screen.getByTestId('summary-total')).toHaveTextContent('$64.92'))
    expect(screen.getByTestId('summary-tax')).toHaveTextContent('$4.95')
    expect(screen.getByTestId('line-total-0')).toHaveTextContent('$59.97')
    const last = previews.at(-1) as { tax_rate: string; items: { quantity: string; unit_price: string }[] }
    expect(last.tax_rate).toBe('8.25')
    expect(last.items[0]).toMatchObject({ quantity: '3', unit_price: '19.99' })
  })

  it('validates required fields before saving', async () => {
    mockSession()
    let posted = false
    server.use(
      http.get('*/api/orgs/:org/customers', () => HttpResponse.json({ data: [customer], total: 1 })),
      http.post('*/api/orgs/:org/invoices', () => {
        posted = true
        return HttpResponse.json({})
      }),
    )
    renderApp(`/o/${ORG_ID}/invoices/new`)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Save as draft' }))
    expect(await screen.findByText('Choose a customer')).toBeInTheDocument()
    expect(screen.getByText('Describe the item')).toBeInTheDocument()
    expect(screen.getByText('Enter a price')).toBeInTheDocument()
    expect(posted).toBe(false)
  })

  it('maps server field errors onto line items', async () => {
    mockSession()
    server.use(
      http.get('*/api/orgs/:org/customers', () => HttpResponse.json({ data: [customer], total: 1 })),
      http.get('*/api/orgs/:org/projects', () => HttpResponse.json({ data: [], total: 0 })),
      http.post('*/api/orgs/:org/invoices/preview', () => HttpResponse.json({ error: { code: 'validation_failed', message: 'x', fields: {} } }, { status: 422 })),
      http.post('*/api/orgs/:org/invoices', () =>
        HttpResponse.json(
          { error: { code: 'validation_failed', message: 'some fields are invalid', fields: { 'items.0.quantity': 'is too large' } } },
          { status: 422 },
        ),
      ),
    )
    renderApp(`/o/${ORG_ID}/invoices/new?customer_id=c1`)
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText('Description'), 'Huge order')
    await user.type(screen.getByLabelText('Unit price'), '1')
    await user.click(screen.getByRole('button', { name: 'Save as draft' }))
    expect(await screen.findByText('is too large')).toBeInTheDocument()
  })
})

describe('invoice detail and payments', () => {
  it('displays the balance and overdue state from the server', async () => {
    mockSession()
    server.use(
      http.get('*/api/orgs/:org/invoices/inv1', () =>
        HttpResponse.json({ invoice: invoiceFixture({ is_overdue: true, display_status: 'overdue', due_date: '2026-10-31' }) }),
      ),
    )
    renderApp(`/o/${ORG_ID}/invoices/inv1`)
    expect(await screen.findByTestId('balance-due')).toHaveTextContent('$600.00')
    expect(screen.getByText(/Overdue since Oct 31, 2026/)).toBeInTheDocument()
    expect(screen.getAllByText('Overdue').length).toBeGreaterThan(0)
  })

  it('reuses one idempotency key when a payment is retried', async () => {
    mockSession()
    const keys: string[] = []
    let calls = 0
    server.use(
      http.get('*/api/orgs/:org/invoices/inv1', () =>
        HttpResponse.json({ invoice: calls >= 2 ? invoiceFixture({ amount_paid: '600.00', balance: '400.00' }) : invoiceFixture() }),
      ),
      http.post('*/api/orgs/:org/invoices/inv1/payments', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key') ?? '')
        calls++
        if (calls === 1) return HttpResponse.json({ error: { code: 'internal', message: 'something went wrong' } }, { status: 500 })
        return HttpResponse.json({ invoice: invoiceFixture({ amount_paid: '600.00', balance: '400.00' }), replayed: false }, { status: 201 })
      }),
    )
    renderApp(`/o/${ORG_ID}/invoices/inv1`)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Record payment' }))
    const dialog = await screen.findByRole('dialog')
    const amount = within(dialog).getByLabelText('Amount (USD)')
    expect(amount).toHaveValue('600.00')
    await user.clear(amount)
    await user.type(amount, '200.00')
    await user.click(within(dialog).getByRole('button', { name: 'Review payment' }))
    expect(await within(dialog).findByText(/received from/)).toHaveTextContent('$200.00')

    await user.click(within(dialog).getByRole('button', { name: 'Confirm payment' }))
    expect(await within(dialog).findByText('something went wrong')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Confirm payment' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(keys).toHaveLength(2)
    expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/)
    expect(keys[1]).toBe(keys[0])
    expect(await screen.findByTestId('balance-due')).toHaveTextContent('$400.00')
  })

  it('shows the server overpayment error on the amount field', async () => {
    mockSession()
    server.use(
      http.get('*/api/orgs/:org/invoices/inv1', () => HttpResponse.json({ invoice: invoiceFixture() })),
      http.post('*/api/orgs/:org/invoices/inv1/payments', () =>
        HttpResponse.json(
          { error: { code: 'overpayment', message: 'payment exceeds the balance', fields: { amount: 'exceeds the outstanding balance of 600.00' } } },
          { status: 422 },
        ),
      ),
    )
    renderApp(`/o/${ORG_ID}/invoices/inv1`)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Record payment' }))
    const dialog = await screen.findByRole('dialog')
    const amount = within(dialog).getByLabelText('Amount (USD)')
    await user.clear(amount)
    await user.type(amount, '700')
    await user.click(within(dialog).getByRole('button', { name: 'Review payment' }))
    await user.click(await within(dialog).findByRole('button', { name: 'Confirm payment' }))
    expect(await within(dialog).findByText('exceeds the outstanding balance of 600.00')).toBeInTheDocument()
  })

  it('hides payment actions from roles without permission', async () => {
    mockSession({ role: 'viewer', permissions: ['invoices.view'] })
    server.use(http.get('*/api/orgs/:org/invoices/inv1', () => HttpResponse.json({ invoice: invoiceFixture() })))
    renderApp(`/o/${ORG_ID}/invoices/inv1`)
    expect(await screen.findByTestId('balance-due')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Record payment' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Reverse/ })).not.toBeInTheDocument()
  })
})
