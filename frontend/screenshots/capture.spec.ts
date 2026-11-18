import { expect, test } from '@playwright/test'

// Captures README screenshots from a running dev stack with demo data:
//   backend:  go run ./cmd/finops seed  &&  go run ./cmd/finops serve
//   frontend: npx vite --host 127.0.0.1
//   then:     npx playwright test -c playwright.screens.config.ts
const out = '../docs/screenshots'

test('capture screenshots of the demo organization', async ({ page }) => {
  test.setTimeout(120_000)
  await page.goto('/login')
  await page.getByLabel('Email').fill('owner@demo.finops.test')
  await page.getByLabel('Password').fill('FinOpsDemo!2026')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
  const base = page.url().replace(/\/overview.*$/, '')

  await page.goto(`${base}/integrations`)
  await expect(page.getByRole('heading', { name: 'QuickBooks Online' })).toBeVisible()
  const connect = page.getByRole('button', { name: 'Connect QuickBooks' })
  if (await connect.isVisible()) {
    await connect.click()
    await page.getByRole('button', { name: 'Sync now' }).click()
    await expect(page.getByText('Succeeded', { exact: true }).first()).toBeVisible({ timeout: 60_000 })
  }

  const shots: [string, string][] = [
    ['overview', '/overview'],
    ['invoices', '/invoices'],
    ['projects', '/projects'],
    ['budgets', '/budgets'],
    ['expenses', '/expenses'],
    ['reports', '/reports?report=monthly-summary'],
    ['reconciliation', '/reconciliation'],
    ['integrations', '/integrations'],
    ['audit-logs', '/audit-logs'],
  ]
  for (const [name, path] of shots) {
    await page.goto(base + path)
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(600)
    await page.screenshot({ path: `${out}/${name}.png`, fullPage: false })
  }

  await page.goto(`${base}/invoices?status=partially_paid`)
  await page.getByRole('row').nth(1).getByRole('link').first().click()
  await expect(page.getByTestId('balance-due')).toBeVisible()
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${out}/invoice-detail.png` })

  await page.goto(`${base}/invoices/new`)
  await page.getByLabel('Customer').selectOption({ index: 1 })
  await page.getByLabel('Description').fill('Quarterly advisory retainer')
  await page.getByLabel('Unit price').fill('4500.00')
  await page.getByLabel('Tax rate (%)').fill('6.25')
  await expect(page.getByTestId('summary-total')).toHaveText('$4,781.25')
  await page.screenshot({ path: `${out}/invoice-editor.png` })

  await page.goto(`${base}/reconciliation`)
  await page.getByRole('row').filter({ hasText: 'Amount differs' }).first().click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${out}/reconciliation-review.png` })
})
