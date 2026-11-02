# Usage analytics

The admin Usage Analytics page reads aggregated data from
`GET /api/v1/analytics/dashboard`. Event ingestion is available to authenticated
app users at `POST /api/v1/analytics/events`; user identity is always taken from
the JWT, never from the request body. Dashboard access requires an admin JWT.

## Event model

The event endpoint accepts an allowlist:

- `resource_opened`
- `resource_download_attempt`
- `resource_download_success`
- `resource_download_failed`
- `resource_download_blocked`
- `search_performed`
- `search_result_opened`

Events include a client-generated id for retry idempotency, an optional resource
or result identity, category/result count, platform, and an optional download
attempt id to correlate its lifecycle. The backend snapshots the authenticated
user's university and department. It ignores client-supplied user IDs and does
not accept or store search query text.

The app's authenticated foreground device heartbeat writes at most one
`app_active` event per user per UTC day. This supports daily active-user trends
and retention cohorts without storing device identifiers in the analytics
table.

## Dashboard definitions

- **Active users:** distinct users with an `app_active` event in the selected
  range.
- **Resource opens:** successful resource-view actions, separate from download
  attempts.
- **Download success:** client-confirmed completion after the file is written
  locally. This is not a server-side verification of file contents.
- **Search quality:** counts and result totals grouped by category; no query
  text is retained. Result-open counts are for resource results.
- **Signup to first action:** new users whose first recorded resource open or
  successful download occurred during the selected range.
- **Day-7 retention:** eligible users with an `app_active` event between six
  and seven days after signup. Accounts younger than seven days are excluded.

Dashboard ranges are limited to 7, 30, or 90 days. Events older than 90 days
are pruned during authenticated app activity writes. Analytics begins
accumulating after deployment; historical per-user activity cannot be inferred
from the existing aggregate resource counters.
