import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { BudgetSummary } from '@/lib/models'
import { BudgetMeter, BudgetWarning, InvoiceStatusBadge, Money } from './data'

const summary = (s: Partial<BudgetSummary>): BudgetSummary => ({
  total: '1000.00',
  actual: '0.00',
  pending: '0.00',
  remaining: '1000.00',
  percent_used: '0.0',
  level: 'ok',
  ...s,
})

describe('budget display', () => {
  it('shows consumption from the server values', () => {
    render(<BudgetMeter summary={summary({ actual: '847.50', percent_used: '84.8', level: 'warning' })} currency="USD" />)
    expect(screen.getByText(/\$847\.50/)).toBeInTheDocument()
    expect(screen.getByText('84.8%')).toBeInTheDocument()
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuenow', '84.8')
  })

  it('caps the bar at 100% when over budget', () => {
    render(<BudgetMeter summary={summary({ actual: '1500.00', percent_used: '150.0', level: 'over', remaining: '-500.00' })} currency="USD" />)
    expect(screen.getByRole('meter')).toHaveAttribute('aria-valuenow', '100')
    expect(screen.getByText('150.0%')).toBeInTheDocument()
  })

  it('labels warning and over-budget states', () => {
    const { rerender } = render(<BudgetWarning summary={summary({ level: 'warning' })} />)
    expect(screen.getByText('Approaching budget')).toBeInTheDocument()
    rerender(<BudgetWarning summary={summary({ level: 'over' })} />)
    expect(screen.getByText('Over budget')).toBeInTheDocument()
    rerender(<BudgetWarning summary={summary({ level: 'ok' })} />)
    expect(screen.queryByText(/budget/i)).not.toBeInTheDocument()
  })
})

describe('financial values', () => {
  it('renders money in the record currency', () => {
    render(
      <>
        <Money amount="1234.5" currency="EUR" />
        <Money amount="980" currency="JPY" />
      </>,
    )
    expect(screen.getByText('€1,234.50')).toBeInTheDocument()
    expect(screen.getByText('¥980')).toBeInTheDocument()
  })

  it('labels invoice states', () => {
    render(
      <>
        <InvoiceStatusBadge status="partially_paid" />
        <InvoiceStatusBadge status="overdue" />
      </>,
    )
    expect(screen.getByText('Partially paid')).toBeInTheDocument()
    expect(screen.getByText('Overdue')).toBeInTheDocument()
  })
})
