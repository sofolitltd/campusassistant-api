# Campus Market: how it works and how to run it

## Orders
All status changes go through `OrderService` (`internal/service/order_service.go`).

- Flow: `pending_payment → paid → processing → shipped → delivered`; cancel from any non-final status. Steps may be skipped forward (cash on delivery starts at `processing`); nothing moves backwards or out of `delivered`/`cancelled`.
- Every status an order enters is recorded in `order_events` (the buyer's timeline, `GET /my/orders/:id` → `events`).
- Buyer and seller notifications (category `marketplace`, respects mutes) are sent after the change commits. Tapping opens `/campusmarket/orders/:id` (buyer) or `/merchant/manage/:merchantId` (seller).
- Buyers can cancel unpaid orders and cash-on-delivery orders that haven't shipped: `POST /my/orders/:id/cancel`. A paid bKash order goes through the admin refund flow.

## Stock
- Checkout reserves stock atomically (`UPDATE … WHERE stock >= qty`); two buyers can't take the last unit. A failed line rolls the whole checkout back (HTTP 409 with a readable message).
- Stock returns when an order is cancelled (buyer, admin, refund of an undelivered order) and when an unpaid bKash order is abandoned for 2 hours (swept by the reconciler in `cmd/api/main.go`).
- Tested on SQLite only; the atomic UPDATE is what protects Postgres, but run a concurrent checkout against a staging DB before relying on it.

## Reviews
- Only buyers with a **delivered** order containing the product can review; one review per product, editable and deletable.
- Sellers can reply; admin can hide (`PUT /reviews/:id/hide`). Hidden reviews don't count.
- `rating_avg` / `rating_count` on products and merchants are maintained by `ReviewService` and are read-only through GORM, so edits can't overwrite them.

## Discovery
- `GET /products-by-location` takes `q`, `min_price`, `max_price`, `in_stock`, `featured`, `sort` (`new|price_asc|price_desc|top_rated|popular`), `limit`, `offset`, `category_id`, `merchant_id`. The body is still a bare array; the match count is in the `X-Total-Count` header. The default order puts featured products first.
- `GET /products-lookup?ids=` revalidates a saved cart. `GET|PUT|DELETE /my/wishlist…` is the wishlist; saved products trigger back-in-stock and price-drop alerts.
- Featuring is admin-only (`PUT /products/:id/feature {days}`; 0 removes it).

## Money
- New-seller promo: `GET|PUT /billing/commission-policy` (admin). During `promo_days` after approval a seller pays `promo_rate` (never more than their own rate). The rate in force is stored on each order line (`commission_rate_snapshot`).
- Sellers: earnings and payouts (`/my/merchants/:id/earnings|payouts`), fee (`/commission`), stats (`/stats?days=`), reviews (`/reviews`).
- Operators: `GET /billing/marketplace-metrics` (also on the admin Billing overview).

## Deploy checklist
1. Run once with `DB_AUTO_MIGRATE=true` (new tables `order_events`, `product_reviews`, `wishlist_items`, `commission_policies`; new columns on `products` and `merchants`).
2. Rebuild the binary (`make build`); the committed one is stale.
3. Ship the API before the app builds: older app builds keep working, they just don't show the new features.
4. In admin → Billing → Commission, turn the promo on if you want it.

## Launch checklist (the part that decides whether it takes off)
- Keep 3–4 categories live (e.g. textbooks, notes & printing, hostel essentials, snacks) and hide the rest in admin → Marketplace → Categories.
- Onboard 10–20 sellers by hand and seed listings through the platform merchant; give each a good cover photo.
- Watch weekly: active sellers, buyers in 30 days, repeat-buyer rate, "delivered smoothly" (admin → Billing overview).
