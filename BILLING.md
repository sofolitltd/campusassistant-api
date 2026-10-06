# Payments, ledger, invoices, payouts, entitlements

All money is whole BDT, the unit sent to bKash.

## Payment flow (subscriptions and marketplace orders)

1. `POST /payments/{bkash|marketplace}/create` — the server prices it, calls bKash, stores an `initiated` transaction. A coupon use is **reserved atomically** here (`UPDATE … WHERE used_count < max_uses`).
2. The user pays in bKash; the app calls `…/execute`.
3. **Fulfilment** (`PaymentService.fulfil`, `MarketplacePaymentService.fulfil`) runs in one DB transaction: row-lock the payment → if already `completed`, return → check bKash's amount equals what we charged → grant/mark paid → write ledger journal → issue invoice. Concurrent executes, retries and the reconciler can't double-grant.
4. If execute fails or reports "not completed", the server asks bKash's status API before deciding (a lost response ≠ unpaid).
5. **Reconciler** (`runPaymentReconciler`, every 5 min, `cmd/api/main.go`): for payments still `initiated` after 2 min it queries bKash. Completed → fulfilled (user paid but the app died). Not completed and >1 h old → cancelled and the coupon use released. bKash unreachable → left for the next pass. It also books earnings for delivered orders whose follow-up failed. Safe on several instances at once.

Payments that bKash confirms but we refuse to auto-fulfil (amount mismatch, order cancelled mid-checkout) become `needs_review` for an admin; the reconciler never touches them.

Renewals stack (buy while Pro → extends from current expiry); a lifetime user is never given a dated expiry.

## Ledger (`journals` + `ledger_entries`)

Append-only, every journal balances, idempotent on `(kind, source_type, source_id)`.

| Event | Debit | Credit |
|---|---|---|
| Subscription paid | `asset:gateway_bkash` | `revenue:subscriptions` |
| Order paid (bKash) | `asset:gateway_bkash` | `liability:orders_escrow` |
| Order delivered (bKash) | `liability:orders_escrow` | `liability:merchant:<id>` (net), `revenue:commission`; platform products → `revenue:platform_sales` |
| Order delivered (COD) | `liability:merchant:<id>` (commission) | `revenue:commission` — merchant holds the cash; balance can go negative |
| Payout requested | `liability:merchant:<id>` | `liability:payouts_pending` |
| Payout paid | `liability:payouts_pending` | `asset:cash_out` |
| Payout rejected | `liability:payouts_pending` | `liability:merchant:<id>` |

Commission uses the rate snapshotted on each order item at checkout. The ledger starts at deploy: orders paid earlier are backfilled lazily when they are delivered; historic subscriptions are not backfilled.

## Endpoints

- User: `GET /my/invoices`, `/my/invoices/:id`, `/my/invoices/:id/print` (printable HTML → "Save as PDF"), `GET /my/entitlements`
- Merchant owner: `GET /my/merchants/:id/earnings`, `GET|POST /my/merchants/:id/payouts`
- Admin (`RequireAdmin`): `GET /billing/{summary,ledger,invoices,payouts}`, `GET /billing/merchants/:id/balance`, `PUT /billing/payouts/:id/{paid,reject}`, `GET|PUT /subscription-plans/:id/entitlements`

Payout rules: merchant approved, payout method/account set, amount ≥ 500 BDT (`DefaultMinPayout`) and ≤ balance. The amount leaves the balance when requested (under a merchant row lock), so it can't be requested twice.

## Refunds

Recorded by an admin in the dashboard (Billing → Payments → Refund) **after** sending the money back from the bKash merchant portal; the admin enters bKash's refund transaction id. The system does not call bKash's refund API.

- Subscription: reverses the revenue entry, voids the invoice, ends that subscription (`refunded_at`), recomputes Pro from the user's remaining non-refunded subscriptions, returns the coupon use.
- Marketplace order (bKash only): reverses the payment and, if already delivered, the merchant earnings and commission (a merchant who was already paid out can go negative; it nets against future earnings); order becomes cancelled; invoice voided.
- Works for `completed` and `needs_review` payments. Idempotent (`already refunded` → 409). Cash-on-delivery orders are cancelled, not refunded.
- Endpoints (admin): `GET /billing/payments?status=`, `GET /billing/refunds`, `POST /billing/refunds/{subscription|order}` with `{id, reference, reason}` (`id` = payment row for subscriptions, order id for orders).

## Admin dashboard

`/billing` in campusassistant-admin: Overview, Payments (refund), Payouts (mark paid / reject), Refunds, Ledger, Invoices, Entitlements (per-plan editor).

## Entitlements

`PlanEntitlement` (plan → feature, limit, period) + `UsageCounter`. A Pro user whose plan has **no** rows gets every `DefaultProFeatures`, unlimited — existing plans behave as before. Gate a route with `middleware.RequireEntitlement(svc, domain.FeaturePremiumContent)`; meter capped features with `EntitlementService.Consume` (atomic, can't exceed the cap). Nothing existing is gated yet.

## Known limits

- Unit tests run on in-memory SQLite: they prove the idempotency/guard logic but **not** Postgres row locking (SQLite ignores `FOR UPDATE`). Run a concurrency smoke test against Postgres before relying on it.
- `QueryPayment` (`/tokenized/checkout/payment/status`) is implemented from bKash's v1.2.0-beta docs and has not been exercised against the live sandbox.
- Merchant-facing screens (earnings, request payout) and customer invoice screens exist as API only; the Flutter app does not use them yet.
- Multi-merchant orders are marked delivered as a whole, releasing every merchant's earnings together.
- Rate limiting is in-memory per instance.
