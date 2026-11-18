import { describe, expect, it } from 'vitest'
import { addDaysISO, formatDate, formatMoney, isNegative, isZero } from './format'

describe('formatMoney', () => {
  it('formats decimal strings without float rounding errors', () => {
    expect(formatMoney('0.10', 'USD')).toBe('$0.10')
    expect(formatMoney('1234567.89', 'USD')).toBe('$1,234,567.89')
    expect(formatMoney('9007199254740993.01', 'USD')).toBe('$9,007,199,254,740,993.01')
  })

  it('respects currency minor units', () => {
    expect(formatMoney('1500', 'JPY')).toBe('¥1,500')
    expect(formatMoney('12.345', 'KWD')).toBe('KWD 12.345')
  })

  it('formats negatives and empty values', () => {
    expect(formatMoney('-50.00', 'EUR')).toBe('-€50.00')
    expect(formatMoney(null, 'USD')).toBe('—')
  })
})

describe('amount helpers', () => {
  it('detects zero and negative decimal strings', () => {
    expect(isZero('0.00')).toBe(true)
    expect(isZero('-0.00')).toBe(true)
    expect(isZero('0.01')).toBe(false)
    expect(isNegative('-0.01')).toBe(true)
    expect(isNegative('-0.00')).toBe(false)
    expect(isNegative('5')).toBe(false)
  })
})

describe('dates', () => {
  it('formats calendar dates without timezone drift', () => {
    expect(formatDate('2026-01-01')).toBe('Jan 1, 2026')
    expect(formatDate('2026-12-31')).toBe('Dec 31, 2026')
  })

  it('adds days across month boundaries', () => {
    expect(addDaysISO('2026-01-31', 1)).toBe('2026-02-01')
    expect(addDaysISO('2028-02-28', 1)).toBe('2028-02-29')
  })
})
