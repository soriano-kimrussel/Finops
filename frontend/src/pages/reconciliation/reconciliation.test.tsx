import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { mockSession, ORG_ID, renderApp, server } from '@/test/utils'
import type { ReconRecord } from './ReconciliationPage'

const record: ReconRecord = {
  id: 'r1',
  entity_type: 'invoice',
  match_status: 'amount_mismatch',
  review_status: 'open',
  decision: null,
  decision_note: null,
  reviewed_by_name: null,
  reviewed_at: null,
  reference: 'INV-000012',
  counterparty: 'Halvorsen Freight Co.',
  currency: 'USD',
  external_amount: '1275.00',
  internal_amount: '1250.00',
  difference: '25.00',
  external_date: '2026-10-02',
  internal_date: '2026-10-02',
  invoice_id: 'inv-12',
  payment_id: null,
  allowed_decisions: ['accepted', 'follow_up', 'ignored'],
  created_at: '2026-11-18T10:00:00Z',
}

describe('reconciliation review', () => {
  it('records a decision with a note', async () => {
    mockSession()
    let posted: unknown = null
    server.use(
      http.get('*/api/orgs/:org/reconciliation', () =>
        HttpResponse.json({ data: posted ? [] : [record], total: posted ? 0 : 1, summary: { amount_mismatch: { open: posted ? 0 : 1 } }, last_run: null }),
      ),
      http.get('*/api/orgs/:org/reconciliation/r1', () => HttpResponse.json({ record, history: [record], external_payload: {} })),
      http.post('*/api/orgs/:org/reconciliation/r1/review', async ({ request }) => {
        posted = await request.json()
        return HttpResponse.json({ record: { ...record, decision: 'accepted', review_status: 'reviewed' } })
      }),
    )
    renderApp(`/o/${ORG_ID}/reconciliation`)
    const user = userEvent.setup()
    const row = await screen.findByRole('row', { name: /INV-000012/ })
    expect(row).toHaveTextContent('Amount differs')
    expect(row).toHaveTextContent('$25.00')
    await user.click(row)

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('$1,275.00')).toBeInTheDocument()
    expect(within(dialog).queryByLabelText('Create invoice from QuickBooks')).not.toBeInTheDocument()
    const submit = within(dialog).getByRole('button', { name: 'Record decision' })
    expect(submit).toBeDisabled()

    await user.click(within(dialog).getByLabelText('Accept difference'))
    await user.type(within(dialog).getByLabelText('Note'), 'Late fee added in QuickBooks only')
    await user.click(submit)

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(posted).toEqual({ decision: 'accepted', note: 'Late fee added in QuickBooks only' })
    expect(await screen.findByText('Nothing to review')).toBeInTheDocument()
  })
})
