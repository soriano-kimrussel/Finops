import { expect, test, type Page } from '@playwright/test'

const stamp = Date.now()
const owner = { name: 'Riley Morgan', email: `riley.${stamp}@example.test`, password: 'correct horse battery staple' }
const outsider = { name: 'Casey Park', email: `casey.${stamp}@example.test`, password: 'another long password 42' }

let orgUrl = ''
let invoiceUrl = ''

async function register(page: Page, who: { name: string; email: string; password: string }) {
  await page.goto('/register')
  await page.getByLabel('Full name').fill(who.name)
  await page.getByLabel('Work email').fill(who.email)
  await page.getByLabel('Password').fill(who.password)
  await page.getByRole('button', { name: 'Create account' }).click()
  await expect(page).toHaveURL(/\/orgs\/new$/)
}

async function createOrg(page: Page, name: string) {
  await page.getByLabel('Organization name').fill(name)
  await page.getByRole('button', { name: 'Create organization' }).click()
  await expect(page).toHaveURL(/\/o\/[0-9a-f-]{36}\/overview$/)
  return page.url().replace(/\/overview$/, '')
}

test.describe.serial('financial operations workflow', () => {
  test('1–2. sign up, log out, log in and create an organization', async ({ page }) => {
    await register(page, owner)
    orgUrl = await createOrg(page, 'Harbor Accounting Group')
    await expect(page.getByRole('button', { name: 'Switch organization' })).toContainText('Harbor Accounting Group')

    await page.getByRole('button', { name: 'Log out' }).click()
    await expect(page).toHaveURL(/\/login$/)
    await page.getByLabel('Email').fill(owner.email)
    await page.getByLabel('Password').fill('wrong password')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText('email or password is incorrect')).toBeVisible()
    await page.getByLabel('Password').fill(owner.password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
  })

  test('3–8. customer, project, invoice, partial payment, balance and dashboard', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel('Email').fill(owner.email)
    await page.getByLabel('Password').fill(owner.password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()

    // 3. Customer
    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Customers' }).click()
    await page.getByRole('button', { name: 'New customer' }).first().click()
    const custDialog = page.getByRole('dialog')
    await custDialog.getByLabel('Company name').fill('Pinecrest Medical Partners')
    await custDialog.getByLabel('Contact name').fill('Jordan Ellis')
    await custDialog.getByLabel('Email').fill('ap@pinecrest.example')
    await custDialog.getByRole('button', { name: 'Create customer' }).click()
    await expect(page.getByRole('heading', { name: 'Pinecrest Medical Partners' })).toBeVisible()
    const customerUrl = page.url()

    // 4. Project
    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Projects' }).click()
    await page.getByRole('button', { name: 'New project' }).click()
    const projDialog = page.getByRole('dialog')
    await projDialog.getByLabel('Project name').fill('Revenue cycle review')
    await projDialog.getByLabel('Customer').selectOption({ label: 'Pinecrest Medical Partners (USD)' })
    await projDialog.getByRole('button', { name: 'Create project' }).click()
    await expect(page.getByRole('heading', { name: 'Revenue cycle review' })).toBeVisible()

    // 5. Invoice, with totals calculated by the server
    await page.goto(customerUrl)
    await page.getByRole('link', { name: 'New invoice' }).click()
    await expect(page.getByRole('heading', { name: 'New invoice' })).toBeVisible()
    await page.getByLabel('Description').fill('Revenue cycle assessment')
    await page.getByLabel('Unit price').fill('1500.00')
    await page.getByLabel('Tax rate (%)').fill('10')
    await expect(page.getByTestId('summary-total')).toHaveText('$1,650.00')
    await page.getByRole('button', { name: 'Save as draft' }).click()
    await expect(page.getByRole('heading', { name: /Invoice INV-000001/ })).toBeVisible()
    invoiceUrl = page.url()

    await page.getByRole('button', { name: 'Mark as sent' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Mark as sent' }).click()
    await expect(page.getByRole('button', { name: 'Record payment' })).toBeVisible()

    // 6. Partial payment, with a confirmation step
    await page.getByRole('button', { name: 'Record payment' }).click()
    const payDialog = page.getByRole('dialog')
    await expect(payDialog.getByLabel('Amount (USD)')).toHaveValue('1650.00')
    await payDialog.getByLabel('Amount (USD)').fill('650.00')
    await payDialog.getByLabel('Reference').fill('ACH 88213')
    await payDialog.getByRole('button', { name: 'Review payment' }).click()
    await expect(payDialog.getByText(/received from/)).toContainText('$650.00')
    await payDialog.getByRole('button', { name: 'Confirm payment' }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)

    // 7. Outstanding balance
    await expect(page.getByTestId('balance-due')).toHaveText('$1,000.00')
    await expect(page.getByText('Partially paid').first()).toBeVisible()

    // An overpayment is refused by the server
    await page.getByRole('button', { name: 'Record payment' }).click()
    const over = page.getByRole('dialog')
    await over.getByLabel('Amount (USD)').fill('1000.01')
    await over.getByRole('button', { name: 'Review payment' }).click()
    await over.getByRole('button', { name: 'Confirm payment' }).click()
    await expect(over.getByText(/exceeds the outstanding balance of 1000.00/)).toBeVisible()
    await over.getByRole('button', { name: 'Cancel' }).click()

    // 8. Dashboard metrics reflect the books
    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Overview' }).click()
    const outstanding = page.getByText('Outstanding invoices').locator('..')
    await expect(outstanding).toContainText('$1,000.00')
    await expect(page.getByText('Invoiced, excluding tax').locator('..')).toContainText('$1,500.00')
    await expect(page.getByText('Payment for INV-000001')).toBeVisible()
  })

  test('9–10. sync QuickBooks (simulated) and review a reconciliation record', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel('Email').fill(owner.email)
    await page.getByLabel('Password').fill(owner.password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()

    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Integrations' }).click()
    await expect(page.getByText('Simulated — sample data').first()).toBeVisible()
    await page.getByRole('button', { name: 'Connect QuickBooks' }).click()
    await expect(page.getByText('Connected', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Sync now' }).click()
    await expect(page.getByText('Succeeded', { exact: true })).toBeVisible({ timeout: 30_000 })
    await expect(page.getByText(/3 invoices \(3 new/)).toBeVisible()

    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Reconciliation' }).click()
    const row = page.getByRole('row').filter({ hasText: 'Invoice QB-1043' })
    await expect(row).toContainText('Only in QuickBooks')
    await row.click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('No matching record')).toBeVisible()
    await dialog.getByLabel('Needs follow-up').check()
    await dialog.getByLabel('Note').fill('Confirm with Westfield whether this contract review was ours to bill.')
    await dialog.getByRole('button', { name: 'Record decision' }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByRole('row').filter({ hasText: 'Invoice QB-1043' })).toHaveCount(0)

    await page.getByLabel('Review').selectOption('reviewed')
    await expect(page.getByRole('row').filter({ hasText: 'Invoice QB-1043' })).toContainText('Needs follow-up')

    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Audit Logs' }).click()
    await expect(page.getByText('reconciliation.decided')).toBeVisible()
    await expect(page.getByText('payment.recorded')).toBeVisible()
  })

  test('11. another organization cannot reach this data', async ({ browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()
    await register(page, outsider)
    const ownOrg = await createOrg(page, 'Casey Consulting')

    await page.goto(invoiceUrl)
    await expect(page).toHaveURL(new RegExp(`${ownOrg.split('/o/')[1]}/overview$`))
    await expect(page.getByText('INV-000001')).toHaveCount(0)

    const orgId = orgUrl.split('/o/')[1]
    const invoiceId = invoiceUrl.split('/invoices/')[1]
    const direct = await page.request.get(`/api/orgs/${orgId}/invoices/${invoiceId}`)
    expect(direct.status()).toBe(404)
    const viaOwnOrg = await page.request.get(`/api/orgs/${ownOrg.split('/o/')[1]}/invoices/${invoiceId}`)
    expect(viaOwnOrg.status()).toBe(404)
    const list = await page.request.get(`/api/orgs/${ownOrg.split('/o/')[1]}/invoices`)
    expect((await list.json()).total).toBe(0)
    await context.close()
  })
})
