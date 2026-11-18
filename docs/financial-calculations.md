# Financial calculations

Every amount is calculated in Go with `shopspring/decimal` (arbitrary precision) and stored as PostgreSQL `NUMERIC`. No monetary value is ever held in a binary float anywhere in the system:
- JSON sends amounts as strings (`"1250.00"`), and the API rejects exponent notation, thousands separators and currency symbols.
- The browser formats those strings with `Intl.NumberFormat`, which accepts decimal strings exactly.
- The only place a number is converted is to size bars in a chart, and the labels next to those bars still show the server's strings.

## Currencies and precision

Each currency has an ISO 4217 number of minor units (`currencies.minor_units`): 2 for USD, EUR, GBP and most others, 0 for JPY, 3 for KWD.

- **Amounts entered by people** (payments, expenses, budgets, fixed discounts) must not have more decimals than the currency allows. `10.005` USD is rejected, not rounded.
- **Unit prices and quantities** may have up to 4 decimals (for example, 0.0125 per message), because the line total is rounded.
- **Stored totals** are always rounded to the currency's minor units, so they fit `NUMERIC(19,4)` exactly. The largest accepted amount has 15 integer digits.

## Rounding rule

**Half away from zero** ("commercial rounding"), to the currency's minor units: 2.345 → 2.35, 2.344 → 2.34, −2.345 → −2.35. This is `decimal.Round`, used through `money.Round`.

## Invoice totals

Computed by `invoices.Compute`, in this order:

1. **Line total** = quantity × unit price, rounded. Each line is rounded on its own.
2. **Subtotal** = the sum of the rounded line totals.
3. **Discount**:
   - *percent*: subtotal × rate ÷ 100, rounded; the rate is 0–100 with up to 4 decimals
   - *fixed*: the amount as entered, which must fit the currency
   - either way it can never exceed the subtotal, and a request that would is rejected
4. **Taxable amount** = subtotal − discount.
5. **Tax** = taxable amount × tax rate ÷ 100, **rounded once** at invoice level, not per line. One rate applies to the whole invoice.
6. **Total** = taxable amount + tax.

The database re-checks `total = subtotal - discount_total + tax_total` and `discount_total <= subtotal` with CHECK constraints, so even a bug in the code cannot store inconsistent totals.

Worked example (USD, 10% discount, 8.25% tax):

| | |
|---|---|
| 3 × 1250.00 | 3750.00 |
| 1.5 × 19.99 = 29.985 | 29.99 |
| Subtotal | 3779.99 |
| Discount 10% = 377.999 | 378.00 |
| Taxable | 3401.99 |
| Tax 8.25% = 280.664175 | 280.66 |
| **Total** | **3682.65** |

Rounding tax once matters. Three lines of 3.33 at 8.25% give 0.82 at invoice level, while rounding per line would give 3 × 0.27 = 0.81.

The browser never repeats this arithmetic. The invoice editor posts the draft to `/invoices/preview` and shows what comes back.

## Payments and balances

- **Balance** = total − amount paid. A payment must be positive, in the invoice's currency, dated no later than today (in the organization's time zone), and **no larger than the balance**, so overpayments are rejected.
- Status follows the money: the first payment makes an invoice *partially paid*, and reaching the total makes it *paid*. The database's CHECK constraint keeps status and amounts consistent.
- **Idempotency.** Every payment request carries an `Idempotency-Key`.
  - The key is checked *after* the invoice row is locked, so a retry always sees the first attempt's result.
  - The same key with the same content returns the original payment (HTTP 200, `Idempotent-Replayed: true`). The same key with different content is rejected with 409.
  - A unique constraint on (organization, key) backs this up.
- **Concurrency.** Payments lock the invoice row (`SELECT … FOR UPDATE`), so concurrent payments are applied one after another and the balance check always sees the latest amount. A test fires ten 30.00 payments at a 100.00 invoice in parallel and asserts exactly three succeed.
- **Atomicity.** The payment row, the invoice's new paid amount and status, the ledger receipt and the audit entry are written in one transaction. Either all of them happen or none do.
- **Reversals, not edits.** A recorded payment is never changed or deleted. Reversing one marks it reversed, reduces the invoice's paid amount (back to *sent* when it reaches zero), and posts an outgoing ledger entry, so both events stay visible.

## Overdue

An invoice is overdue when it is *sent* or *partially paid* and its due date is before today **in the organization's time zone**. Overdue is computed when read, never stored, so it cannot go stale. Drafts, paid and void invoices are never overdue.

## Expenses and budgets

- Expenses count toward budgets and reports **only once approved**. Pending amounts are shown separately and never mixed into "actual". Rejected expenses don't count at all.
- An approved expense is posted to the cash ledger on its expense date. In this model, approval means the money has been spent or reimbursed.
- **Budget allocation.** A project's budget is the sum of its lines. Each approved expense counts against the line for its category. If its category has no line, it counts against the project's general line; with no general line, it is reported as *unbudgeted*.
- **Consumption** = actual ÷ budget × 100, rounded to one decimal for display. The thresholds use the exact ratio, not the rounded figure:
  - below 80%: OK
  - from 80% up to and including 100%: *warning*
  - above 100%: *over*

  For example, 799.99 of 1000 shows "80.0%" but is still OK.
- Budgets, approved expenses and project-linked invoices must use the project's currency, which the database enforces through a composite foreign key.

## Revenue, expenses and cash: definitions used by reports

| Figure | Definition |
|---|---|
| **Revenue** (accrual) | subtotal − discount of every sent, partially paid or paid invoice, by **issue date**. Excludes tax (collected for the tax authority) and draft and void invoices. |
| **Expenses** | approved expenses, by **expense date** |
| **Net income** | revenue − expenses |
| **Cash in / out** | from the cash ledger: payments received and reversed, and approved expenses, by the date of each movement |
| **Outstanding** | total − paid of every sent or partially paid invoice, **as of today**, whatever the date filter |
| **Overdue** | outstanding invoices past due today |
| **AR aging** | outstanding balances by days past due: current, 1–30, 31–60, 61–90, over 90. It uses current balances, not historical ones. |

All of these are aggregated by PostgreSQL in one currency, the organization's base currency. There is no currency conversion. Records in other currencies are left out of the totals, and the dashboard says how many were left out.

## Assumptions and limits

- One tax rate per invoice. QuickBooks invoices imported during reconciliation keep the tax-inclusive total QuickBooks reports, as one line.
- No currency conversion.
- The ledger is single-entry cash only. Accrual revenue comes from invoices, not from ledger postings.
- Monetary columns allow 4 decimals; currencies with more than 3 minor units are not supported.
