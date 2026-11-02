# Roles & Moderation — Product Requirements

> **Status:** Parts 0–2 are implemented in source. Production migration completed 2026-11-02. API/client deployment, unclaimed-code rotation and pilot rollout remain pending. Part 3 is deferred.
> **Repos touched:** `campusassistant-api` (Go), `campusassistant-admin` (Next.js), `campusassistant` (Flutter).
> **Priority order:** Part 0, then 1, then 2. Part 3 is a to-do for later.

| Part | What | Priority |
|---|---|---|
| 0 | Stop leaking verification codes | Blocker, ship first |
| 1 | Batch / Department / University Moderators | **P1** |
| 2 | Audit log | **P2** |
| 3 | Admin staff roles (RBAC) | Later, see the to-do list |

---

## Background

One admin does all of these today, through the dashboard:

- sends signup verification codes to students,
- reviews document-based student verification requests,
- approves resources (books, notes, questions) that students submit.

That doesn't scale. The goal is to hand these jobs to trusted students at the batch, department and university level, so they can do them on their phones, without giving anyone dashboard access.

### Why the role is not called "CR"

A **CR** (Class Representative) is an official university post. The app keeps CRs as **information only** (the `crs` table and the CR directory). Not every official CR will work in the app, and the person we pick to maintain a batch may not be the CR. Using one name for both would confuse everyone, so the app role is a separate thing called **Moderator**.

| Term | Meaning | Grants app powers? |
|---|---|---|
| CR | The university's official class representative | No, it is directory info only |
| Batch Moderator | A student we pick to look after their own batch | Yes |
| Department Moderator | A student who looks after their whole department | Yes |
| University Moderator | A student who looks after their whole university | Yes |

---

## Part 0: Stop leaking verification codes (blocker)

**Problem.** `GET /students` and `GET /students/:id` (policy `AuthJWT`, generic handler) return the full `Student` row, including `verification_code`. Any logged-in user can list unclaimed students' codes and claim someone else's profile. `GET /teachers` and `GET /staffs` have the same shape.

**Fix.**
- Blank `verification_code` in student, teacher and staff responses unless the caller is an admin.
- Admin routes, and the new moderator endpoints in Part 1, are the only places that return codes.
- Rotate codes for every unclaimed profile after deploying (`POST /students/rotate-unclaimed-codes`). The endpoint requires explicit confirmation, returns only the number rotated, and never returns replacement codes. Anyone who has already read the old codes will then hold stale codes.

**Acceptance.** A student token calling `GET /students?batch_id=…` gets `"verification_code": ""` on every row. An admin token gets the real codes.

---

## Part 1: Moderators (P1)

### 1.1 Principles

1. **Role ≠ identity.** `User.Role` stays `student`. Moderator powers come from separate assignment rows.
2. **One person can hold many roles.** For example: Batch Moderator of CSE-21, Department Moderator of CSE, and University Moderator of DU, all at once. Each role has its own term and can be revoked on its own.
3. **Powers flow down.** A higher scope covers everything beneath it: university covers its departments, and a department covers its batches.
4. **Only admins assign.** Moderators cannot create other moderators in v1.
5. **Only students, and only inside their own campus.** The scope must contain the student's own batch. Their batch, their department, or their university are the only three choices.
6. **The app is the panel.** Moderators work in the Flutter app. The admin dashboard is only for assigning roles, oversight, and overrides.
7. **The server decides.** The app hides buttons it can't use, but every endpoint re-checks permissions. Checks hit the DB on every request, so a revoke takes effect immediately with no re-login.

### 1.2 Permissions

Permissions are strings defined in code (new package `internal/authz`). A role is a set of permissions. Handlers never ask "is this a department moderator?". They ask "can this actor do `X` for this batch or department?". Part 3 reuses the same mechanism for admin staff.

| Permission | What it allows | Batch | Department | University |
|---|---|:-:|:-:|:-:|
| `codes.view` | See unclaimed students and their codes, and share them | ✅ | ✅ | ✅ |
| `codes.regenerate` | Issue a new code for an unclaimed student | ✅ | ✅ | ✅ |
| `verifications.review` | Approve or reject document verification requests | ✅ | ✅ | ✅ |
| `class_notices.manage` | Create, edit and delete class notices for a batch | ✅ | ✅ | ✅ |
| `class_slots.manage` | Update class slot status for a batch | ✅ | ✅ | ✅ |
| `resources.review` | Approve or reject submitted resources | — | ✅ | ✅ |

`resources.review` is department and up only, because a `Resource` belongs to a department, not to a batch.

Admins pass every check.

### 1.3 Data model

New table `moderators` in `internal/domain/moderator.go`. It goes into `AutoMigrate`, which means `make migrate`.

```go
type ModeratorScope string

const (
    ModeratorScopeBatch      ModeratorScope = "batch"
    ModeratorScopeDepartment ModeratorScope = "department"
    ModeratorScopeUniversity ModeratorScope = "university"
)

type Moderator struct {
    Base
    UserID       uuid.UUID      // the student's account
    User         *User
    ScopeType    ModeratorScope // batch | department | university
    ScopeID      uuid.UUID      // batch, department or university ID
    UniversityID uuid.UUID      // denormalised for filtering and listing
    DepartmentID *uuid.UUID     // set for batch and department scopes
    TermStart    *time.Time     // nil = starts immediately
    TermEnd      *time.Time     // nil = open-ended
    IsActive     bool           // false once revoked; the row is kept for history
    GrantedBy    *uuid.UUID     // admin ID
    RevokedBy    *uuid.UUID
    RevokedAt    *time.Time
    Note         string         // e.g. "backfilled from CR", "semester 2026"
}
```

- Partial unique index on `(user_id, scope_type, scope_id) WHERE is_active AND deleted_at IS NULL`. The same role can't be active twice, but history rows are allowed.
- `ActiveModerator(db)` scope: `is_active AND (term_start IS NULL OR term_start <= now) AND (term_end IS NULL OR term_end > now)`. This is the single definition of "is a moderator right now". It mirrors today's `ActiveCR`.

### 1.4 Authorisation core (`internal/authz`)

```go
type Target struct{ UniversityID, DepartmentID, BatchID uuid.UUID } // zero = not applicable

// Can is the only permission check handlers use.
func Can(ctx, db, actor Actor, perm Permission, t Target) bool

// Scopes lists the actor's active roles, so list endpoints can filter rows.
func Scopes(ctx, db, userID) []ModeratorRole
```

- `Can` runs **one query**: active rows for the user where `(batch, t.BatchID) OR (department, t.DepartmentID) OR (university, t.UniversityID)`. It then checks `perm` against the matched scope's permission set.
- List endpoints build `WHERE batch_id IN (…) OR department_id IN (…) OR university_id IN (…)` from `Scopes`.
- Unit-test `authz` (table-driven tests over scope × permission × term).

### 1.5 Migrating off `ActiveCR`

Today `class_notice_handler.go` and `class_slot_handler.go` authorise through `ActiveCR`, and `AuthHandler.markCR` sets `user.is_cr`.

1. **Backfill.** Add a `once(db, "backfill_moderators_from_crs", …)` step in `RunMigrations`. Every `ActiveCR` row whose holder resolves to a user (via `user_id`, or through `target_student → students.user_id`) becomes a `batch` moderator for `cr.BatchID`, with `term_end = cr.TermEnd` and `note = "backfilled from CR"`. Rows that conflict are skipped. Current CRs keep posting notices without interruption.
2. Switch class notice and class slot authorisation to `authz.Can(class_notices.manage / class_slots.manage, batch)`.
3. **User payload.** Add `moderator_roles: [{id, scope_type, scope_id, scope_name, term_end, permissions}]`. Keep `is_cr`, but redefine it as **"has any active moderator role"** so older app builds still show the class notice button to the right people. Mark it deprecated, and remove it after the minimum app version passes the release that reads `moderator_roles`.
4. The `crs` table stays as the official CR directory. It no longer grants any power.

### 1.6 API

**Admin, `AdminJWT`:**

| Method | Path | Notes |
|---|---|---|
| GET | `/moderators` | Filters: `university_id`, `department_id`, `batch_id`, `user_id`, `scope_type`, `active` |
| POST | `/moderators` | `{user_id, scope_type, term_start?, term_end?, note?}`. **The server derives `scope_id`** from the student's own batch, department or university, so admins cannot pick an out-of-campus scope. Rejects non-students, unclaimed accounts and duplicates. |
| PATCH | `/moderators/:id` | Change `term_start`, `term_end` or `note` |
| POST | `/moderators/:id/revoke` | Sets `is_active=false`, `revoked_by` and `revoked_at`. There is no hard delete. |

**Moderator, JWT + `authz.Can` on every call:**

| Method | Path | Permission |
|---|---|---|
| GET | `/moderation/roles` | none. Returns the caller's active roles plus pending counts for each tab. |
| GET | `/moderation/students?scope_type&scope_id&q&page` | `codes.view`. Lists unclaimed students in scope with codes. Searchable by name or roll. |
| POST | `/moderation/students/:id/generate-code` | `codes.regenerate`. Reuses `StudentHandler.GenerateCode`. |
| GET | `/moderation/verifications?status=pending` | `verifications.review` |
| GET | `/moderation/verifications/:id` | `verifications.review`. Returns presigned document URLs with a short TTL (≤10 min). |
| PUT | `/moderation/verifications/:id/approve` | `verifications.review` |
| PUT | `/moderation/verifications/:id/reject` | `verifications.review`. A reason is required. |
| GET | `/moderation/resources?status=pending` | `resources.review` |
| PATCH | `/moderation/resources/:id/approve` | `resources.review` |
| PATCH | `/moderation/resources/:id/reject` | `resources.review`. A reason is required. |

Implementation notes:

- Reuse the existing `StudentVerificationHandler` and `ResourceHandler` methods. Pull their core logic into functions that take an "allowed?" callback, so the admin and moderator routes share one code path. Admin routes stay exactly as they are.
- **Conflicts escalate.** If an approval hits a conflict (for example, the roll is already claimed), the moderator sees "Sent to admin". The request stays pending with an `escalated` flag. Moderators never resolve conflicts.
- A moderator cannot review their own submission.
- Rate-limit `generate-code` per moderator (for example, 30 per hour).
- `ReviewedByID` on resources and verifications records the moderator's user ID.

### 1.7 Notifications

- **New verification request:** push to the batch's moderators. If the batch has none, go to department moderators, then university moderators. The existing Telegram admin alert stays.
- **New pending resource:** push to department moderators, falling back to university moderators.
- **Role assigned or revoked:** push to the student ("You're now Batch Moderator of CSE-21").
- **Approval or rejection:** the student is notified exactly as today (`notifyStatus`).

### 1.8 Admin dashboard (`campusassistant-admin`)

- `lib/api.ts`: add a `Moderator` interface and an `api.moderators.{getAll, create, update, revoke}` namespace.
- **New page `/moderators`** (add it to `lib/nav-items.ts`): a table with filters for university, department, batch, scope and active/expired. Each row shows the student, scope, term, granted-by and status.
- **"Assign moderator" modal:** search a student by name, roll or email. Pick a scope from three radio buttons, prefilled from that student's own batch, department and university. Set an optional term and note, then save.
- **Student modal** (`app/universities/[id]/departments/[...slug]/components/StudentModal.tsx`): add a "Moderator roles" section that lists the student's roles with Revoke buttons and an "+ Add role" button.
- Admins keep full access to `/verifications` and resources. The moderator who reviewed an item appears on it as "Reviewed by".

### 1.9 Mobile app (`campusassistant`)

- `User` entity: parse `moderator_roles`, and add `isModerator` and `can(permission, scope)` helpers.
- **Profile page:** a "Moderator Panel" tile, shown only to moderators, with a pending-count badge. The profile badge shows the highest role, for example "Department Moderator". Tapping it lists every role.
- **Moderator Panel screen:**
  - A scope selector at the top, hidden when the user has only one role.
  - **Codes** tab: unclaimed students, a search box, **Copy** and **Share** (system share sheet with a pre-filled message containing the code and the app link), and **Regenerate** (with a confirmation).
  - **Requests** tab: pending verification requests. Opening one shows the documents, with Approve and Reject (reason required).
  - **Resources** tab: shown only with `resources.review`. Pending resources with Preview, Approve and Reject.
- **Class notices and slots:** switch gating from `isCr` to `user.can('class_notices.manage', batch)`.
- Follow the repo's existing rules: build with `make build-*` / `--dart-define-from-file`, and run `dart format` only on edited files.

### 1.10 Rollout

1. **API:** Part 0, then the domain, `authz`, endpoints and backfill. Run `make migrate` (this **is** the production migration; the local backend also writes to prod), then deploy.
2. Rotate unclaimed codes (Part 0).
3. **Admin:** the `/moderators` page and the Student modal section. Assign the first moderators by hand in one or two pilot departments.
4. **App:** release with the Moderator Panel. Older builds keep working through the `is_cr` alias.
5. After the minimum app version is bumped, drop the `is_cr` alias.

### 1.11 Acceptance criteria

- [ ] A student with no role gets 403 on every `/moderation/*` call.
- [ ] A Batch Moderator of CSE-21 sees only CSE-21 students and requests, and gets 403 on a CSE-22 student's code.
- [ ] A Department Moderator of CSE sees every CSE batch and can review CSE resources. A Batch Moderator cannot review resources.
- [ ] One user holding batch, department and university roles sees the union. Revoking the university role leaves the other two working immediately.
- [ ] Expired or future-dated roles grant nothing.
- [ ] Admins cannot assign a role outside the student's own campus, and cannot assign one to a teacher or staff account.
- [ ] After backfill, every previously active CR with an account can still post class notices.
- [ ] Approving a conflicting verification request escalates it instead of failing silently.

### 1.12 Success metrics

- Share of new student claims that use moderator-shared codes.
- Median time a verification request stays pending (target: under 24 hours).
- Share of verifications and resources reviewed by admins (should fall steadily).

---

## Part 2: Audit log (P2)

### Problem

`audit_logs` (`domain.AuditLog`) exists and is migrated, but **no code writes to it**. Nobody can answer "who approved this?" or "who changed this student's name?". `system_logs` is a different thing: it records failed requests only. Once moderators can approve students, this is required.

### Design

**Reshape `AuditLog`.** The table is empty, so confirm the row count before altering it.

```go
type AuditLog struct {
    Base
    ActorType    string          // "admin" | "user" (Admin and User are separate tables)
    ActorID      uuid.UUID
    ActorLabel   string          // name/email snapshot, kept even if the account is deleted
    ViaRole      string          // "admin" | "batch_moderator" | … : which power was used
    Action       string          // "moderator.assign", "verification.approve", "student.update", …
    EntityType   string
    EntityID     uuid.UUID
    UniversityID *uuid.UUID      // scope columns so admins (and later Dept Moderators) can filter
    DepartmentID *uuid.UUID
    BatchID      *uuid.UUID
    Changes      datatypes.JSON  // {field: [before, after]} for updates; redacted
    Reason       string          // rejection reasons etc.
    IPAddress    string
    UserAgent    string
}
```

**How rows get written:**

1. **Explicit calls** to `audit.Record(c, Event{…})` for every high-value action: moderator assign, revoke and edit; verification approve and reject; resource approve and reject; code regenerate; class notice create, edit and delete; admin create and deactivate; notification broadcast; subscription grant; refunds. Write inside the same DB transaction as the change where one exists.
2. **Automatic coverage for generic CRUD:** `GenericHandler` already has hooks (`AfterUpdate`, used by `linked_user_name.go`). Add Create, Update and Delete hooks that record a diff for every generically routed resource. That covers most of the dashboard with no per-resource code.
3. **Not** a blanket "log every request body" middleware. It is noisy, stores personal data, and `system_logs` already covers failures.

**Rules:**

- **Redact:** never store `password_hash`, tokens or `verification_code` values (log "code regenerated", not the code), and never store document URLs.
- **Append-only:** no update or delete endpoint. If needed, revoke `UPDATE`/`DELETE` on the table from the app's DB role.
- **Retention:** keep for 2 years, with a monthly prune job alongside the existing background sweeps.
- An audit write failure must not fail the user's action. Log it to stderr and `system_logs`, except for moderator assign and revoke, which should be transactional.

**Read side:**

- `GET /audit-logs` (admin) with filters for actor, action, entity, scope and date range.
- Admin `/logs` page: add an **Audit** tab next to the system logs. Entity detail views (student, verification, resource) get a small "History" list.
- Later: Department Moderators see actions taken inside their department.

### Acceptance

- [ ] Every action in the explicit list above produces exactly one row with the correct actor, `ViaRole` and scope.
- [ ] An admin edit to a generic resource records only the fields that changed.
- [ ] No row ever contains a password hash, token, verification code or document URL.

---

## Part 3: Admin staff roles. To-do, after Parts 1–2

> Do this before hiring the first dashboard staff member. Until then every dashboard login has full access: `Admin.Role` is `super_admin` or `admin`, and both pass `IsAdminRole`. About 115 routes use the yes/no `AdminJWT` check.

**What Parts 1–2 already put in place:** permission strings and `authz.Can` (§1.4), the actor and scope model, and the audit log that records `ViaRole`. Part 3 adds admin staff as a second kind of actor on the same core.

**Industry reference:** start with **fixed preset roles** (Stripe-style). Model them internally as permission bundles so **custom roles** (Google Workspace / Shopify-style) can be added later without touching handlers. Staff accounts stay separate from student accounts, so a compromised student account can never escalate.

**Proposed preset roles:**

| Role | Covers |
|---|---|
| Super Admin | Everything, including managing staff |
| Academic Ops | Universities, departments, batches, students, teachers, staff, verifications, moderator assignment |
| Content | Resources and books, notices, courses, skills, syllabus |
| Marketplace Ops | Merchants, products, orders, delivery |
| Finance | Subscriptions, payments and bKash, coupons, billing, refunds |
| Support | Mostly read-only on users, plus verification review |
| Marketing | Notifications, banners, referrals, access grants |

A staff member can hold several roles. Each role can optionally be limited to one university (for example, "Academic Ops, DU only").

### Checklist

- [ ] `AdminRoleAssignment{admin_id, role, university_id?, granted_by, revoked_at}`, mirroring `Moderator`.
- [ ] Map each dashboard section to permission strings, and define the preset roles as permission bundles in `authz`.
- [ ] Extend `middleware.Access` with a required permission. Since a policy is already mandatory per route (compile-time), make the permission mandatory too, then replace `AdminJWT` on all ~115 routes, including `registerRoutes[T]`.
- [ ] Also gate the routes that use `middleware.RequireAdmin()` (for example `/subscriptions/admin-grant`).
- [ ] `/auth/admin-login` or `/admins/me` returns the admin's permissions.
- [ ] Dashboard: add a `permission` field to each `lib/nav-items.ts` item, so the sidebar, mobile drawer and command palette hide what the user can't use. Hide action buttons too, and guard each page. The API remains the real gate.
- [ ] `/admins` UI: invite staff, assign and revoke roles, deactivate. Change `POST /admins` so it no longer defaults to `role: "admin"` (which is full access); a new staff member gets nothing until roles are assigned.
- [ ] Admin session invalidation on deactivate. Check whether admin tokens honour a `token_version` like user tokens do; add it if not.
- [ ] TOTP 2FA for every staff login.
- [ ] Guard: the last active Super Admin cannot be removed or deactivated.
- [ ] Later, optional: a custom role editor, and two-person approval for large refunds and mass notifications.

---

## Open questions

1. Max moderators per batch? Suggested: no hard limit in code; admin judgement (2 per batch is a sensible default).
2. Should Department Moderators be able to *nominate* Batch Moderators for admin approval? This is the natural next step after v1. The `granted_by` field already supports it.
3. Should moderators ever edit student records (name, roll)? Not in v1. The name lock from the signup redesign stays admin-only.
4. Teacher moderators? Not in v1. It would only take relaxing the "students only" rule in `POST /moderators`.
5. Confirm production runs with `ACCESS_ENFORCE=true` (the local `.env` does). In log-only mode `AdminJWT` routes are not actually blocked, and none of the above holds.
