# Spec C — Subscription Billing

**Status:** Design approved, ready for implementation planning.
**Date:** 2026-04-21
**Scope:** Replace Spec A's `ai_subscription_active` admin-flip stub with real payment rails on the web (Stripe Checkout + Customer Portal). Single paid tier ("Pro"), monthly and annual billing, 7-day card-required free trial, EUR-only at launch. Web only — mobile IAP (Apple + Google) is a later spec (Spec C.1). Supersedes Spec A's `/admin/set-ai-subscription` endpoint.

Not in scope: mobile in-app purchases, multi-currency pricing, purchasing-power-parity adjustments, tiered plans (Pro / Pro+), quota top-ups / credit packs, student discounts, family plans, referral rewards, public refund policy, email campaigns / abandoned-cart recovery.

---

## 1. Purpose

StudBud gates AI features behind an entitlement flag (`ai_subscription_active`). Spec A shipped this as an admin-flipped boolean so the AI pipeline could be built in parallel with billing. This spec wires that flag to real Stripe subscriptions so users can self-serve their way to Pro, and introduces the local state, audit trail, and recovery tooling required to operate billing safely in production.

Outcomes:
- A free user can click "Start 7-day trial," pay via Stripe Checkout, and have AI access within seconds.
- Cancellations, payment failures, and admin comps all flow through the same `user_subscriptions` table.
- Webhook loss, out-of-order delivery, and duplicate events cannot corrupt entitlement state.
- Spec A's admin endpoint is removed; its functional replacement is a comp-grant endpoint writing to the same table.

## 2. Product Decisions (Locked)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| Q1 | Platform scope at launch | Web only (Stripe). Mobile IAP deferred to Spec C.1. | Fastest proven billing pipeline. Pre-launch, App Store paywall friction is acceptable. |
| Q2 | Tier shape | Single paid tier ("Pro"). | No usage data yet to justify tier splits. Adding Pro+ later is additive, not migratory. |
| Q3 | Billing cadence | Monthly + Annual (annual ~29% off). Two Stripe price IDs under one product. | Monthly = try-before-commit. Annual = retention + revenue smoothing. Standard SaaS dual offer. |
| Q4 | Free trial | 7-day trial, card required upfront. One per user. | Card-required trials convert meaningfully better. Stripe owns the lifecycle; no custom expiry logic. |
| Q5 | Cancellation behavior | Downgrade at period end. No refund on cancel. | Stripe default; removes refund-abuse vectors; matches user expectations. |
| Q6 | Payment-failure dunning | Immediate pause of subscription. No retries, no grace. Access revoked until user updates card and Stripe resumes. | Cleanest possible signal. No revenue recovery from retries is traded for total simplicity + zero abuse surface. |
| Q7 | Checkout infrastructure | Stripe Checkout (hosted) + Stripe Customer Portal (hosted). | ~90% less code than Elements. SCA, tax, Apple/Google Pay all handled. Brand equity not yet worth optimizing. |
| Q8 | Pricing & currency | Single currency (EUR). €6.99/mo, €59.99/yr. Tax-inclusive. | Pre-launch, per-market anchoring is premature. Non-EUR users pay EUR at their card's FX. |
| Q9 | Entitlement source of truth | Local `user_subscriptions` row mirrors Stripe state. `user_has_ai_access(uid)` SQL helper is the entitlement check. Append-only `billing_events` audit log. | Cheap reads, zero Stripe dependency on every AI call, full self-serve debugging. |
| Q10 | Tax | Stripe Tax enabled. Prices displayed tax-inclusive. | Near-zero-effort EU VAT compliance via Stripe's tax partners. |
| Q11 | Refunds & chargebacks | No public refund policy. Manual discretionary refunds via Stripe dashboard. Chargebacks <€50 eaten, >€50 fought with usage evidence. | Industry-standard for indie SaaS. Public policies defer until support bandwidth justifies. |
| Q12 | Webhook reliability | Signature-verify + idempotency by `event.id` (unique constraint) + nightly reconciliation cron + user-facing refresh endpoint, all sharing one Stripe-retrieve path. | Defense in depth without code duplication. Refresh button flips many support tickets into self-service. |
| Q13 | Paywall placement | Inline paywall (reuses Spec A's `PaywallCard.vue`) + public `/pricing` route. Both call the same `/billing/checkout`. | Inline captures impulse conversions; `/pricing` handles considered ones and doubles as marketing. |
| Q14 | Admin backdoor | Keep admin endpoint (env-gated), now writes `user_subscriptions` rows with `status='comped'`. `ADMIN_API_ENABLED=true` required. | One source of truth. Comp audit trail identical to paid-customer audit trail. |
| Q15 | Env config & test-mode isolation | `STRIPE_MODE=test\|live` + key-prefix assertion at boot + per-webhook `livemode` field check. | Three independent safeties. The livemode check specifically blocks cross-environment webhook misrouting. |

## 3. Architecture Overview

### 3.1 Module map

**Backend (`study_buddy_backend/`):**
- `pkg/billing/` — Stripe client wrapper, webhook signature verification, mode-isolation checks (key prefix + livemode).
- `api/service/billingService.go` — `CreateCheckoutSession`, `CreatePortalSession`, `HandleWebhookEvent`, `RefreshFromStripe`, `GrantComp`, `RevokeComp`. All writes to `user_subscriptions` + `billing_events` live here.
- `api/handler/billingHandler.go` — `POST /billing/checkout`, `POST /billing/portal`, `POST /billing/webhook`, `POST /billing/refresh`, `GET /billing/subscription`, `GET /billing/plans`.
- `api/handler/adminHandler.go` — `POST /admin/comp-subscription`, `DELETE /admin/comp-subscription`. Removes the old `POST /admin/set-ai-subscription`.
- `api/cron/billingReconcile.go` — nightly job.
- `api/migrations/` — creates `user_subscriptions`, `billing_events`, `user_has_ai_access()`; backfills; drops `users.ai_subscription_active`.

**Frontend (`studbud/src/`):**
- `api/billing.ts` — client for checkout / portal / subscription / refresh.
- `stores/billing.ts` — Pinia store for current subscription state.
- `pages/PricingPage.vue` — public `/pricing`.
- `pages/BillingPage.vue` — authed `/billing`.
- `components/ai/PaywallCard.vue` — **existing stub from Spec A**, updated to call `/billing/checkout`.
- Navigation additions to Profile + QuotaBadge.

### 3.2 Hard boundaries

- Only `billingService` writes to `user_subscriptions` and `billing_events`.
- Webhook handler and reconciliation cron and refresh endpoint all funnel through `billingService.applyStripeState(user, stripeSub)` — one write path, three entry points.
- `users.ai_subscription_active` column is **removed** after migration. The only way to ask "does this user have AI access?" is `user_has_ai_access(uid)` (or the equivalent Go helper that calls it).
- Frontend never talks to Stripe's API directly (only via redirects to Stripe-hosted pages).
- Price IDs live in env config keyed by plan name. No hard-coded IDs in source.

## 4. Data Model

### 4.1 `user_subscriptions`

```sql
CREATE TABLE user_subscriptions (
    user_id              BIGINT       PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    stripe_customer_id   TEXT         UNIQUE,
    stripe_sub_id        TEXT         UNIQUE,
    status               TEXT         NOT NULL CHECK (status IN (
                                         'trialing','active','past_due','paused',
                                         'canceled','incomplete','incomplete_expired',
                                         'comped'
                                       )),
    plan                 TEXT         NOT NULL CHECK (plan IN ('pro_monthly','pro_annual','comp')),
    current_period_end   TIMESTAMPTZ,
    trial_end            TIMESTAMPTZ,
    cancel_at_period_end BOOLEAN      NOT NULL DEFAULT FALSE,
    paused_at            TIMESTAMPTZ,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_subs_status ON user_subscriptions (status);
CREATE INDEX idx_user_subs_period_end ON user_subscriptions (current_period_end)
    WHERE status IN ('active','trialing','past_due');
```

- `stripe_customer_id` and `stripe_sub_id` are NULL for comped rows.
- One row per user (PK on `user_id`); upsert semantics.
- `status='paused'` and `'past_due'` explicitly **do not** grant access.

### 4.2 Entitlement helper

```sql
CREATE FUNCTION user_has_ai_access(uid BIGINT) RETURNS BOOLEAN
LANGUAGE SQL STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM user_subscriptions
        WHERE user_id = uid
          AND status IN ('active','trialing','comped')
          AND (current_period_end IS NULL OR current_period_end > NOW())
    );
$$;
```

Go helper mirrors this — either a direct SQL call or an equivalent predicate against the cached subscription row.

### 4.3 `billing_events`

```sql
CREATE TABLE billing_events (
    id                BIGSERIAL PRIMARY KEY,
    stripe_event_id   TEXT      UNIQUE,
    user_id           BIGINT    REFERENCES users(id) ON DELETE SET NULL,
    event_type        TEXT      NOT NULL,
    livemode          BOOLEAN   NOT NULL,
    payload           JSONB     NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_billing_events_user ON billing_events (user_id, received_at DESC);
```

- `stripe_event_id` UNIQUE is the idempotency guard. Duplicate webhook delivery conflicts on insert → handler short-circuits with 200 OK.
- `stripe_event_id` is NULL for non-Stripe-originated events (admin actions, cron reconciliations). Those use synthetic `event_type` values: `admin_comp_granted`, `admin_comp_revoked`, `cron_reconciled`, `user_refresh_triggered`.
- `livemode` recorded from the Stripe event (or set to the configured mode for admin/cron events).

### 4.4 Migration from Spec A

1. Create `user_subscriptions`, `billing_events`, `user_has_ai_access()`.
2. Backfill: `INSERT INTO user_subscriptions (user_id, status, plan, current_period_end) SELECT id, 'comped', 'comp', NULL FROM users WHERE ai_subscription_active = TRUE;`
3. Replace `users.ai_subscription_active` reads in the AI pipeline with `user_has_ai_access(user_id)`.
4. Drop `users.ai_subscription_active` column.

All comped-at-migration users keep indefinite access (`current_period_end=NULL`), preserving existing dev/QA beta access.

## 5. Backend Endpoints

### 5.1 `POST /billing/checkout`

**Auth:** required.
**Body:** `{ plan: "pro_monthly" | "pro_annual" }`
**Returns:** `{ url: string }`

Flow:
1. Look up `user_subscriptions.stripe_customer_id` for the user. If absent, create a Stripe Customer (`email=user.email`, `metadata={userId}`) and persist.
2. Refuse if user already has `status IN ('trialing','active')` — return 409 with `{ kind: "already_subscribed" }`. Frontend redirects to `/billing` instead.
3. Map `plan` → price ID via env config `STRIPE_PRICE_PRO_MONTHLY` / `STRIPE_PRICE_PRO_ANNUAL`.
4. Create Stripe Checkout Session:
   - `mode='subscription'`
   - `customer=<stripe_customer_id>`
   - `line_items=[{ price, quantity: 1 }]`
   - `subscription_data={ trial_period_days: 7, metadata: { userId } }`
   - `payment_method_collection='always'`
   - `automatic_tax={ enabled: true }`
   - `tax_id_collection={ enabled: true }`
   - `success_url=<APP_URL>/billing?status=success&session_id={CHECKOUT_SESSION_ID}`
   - `cancel_url=<APP_URL>/pricing?status=cancelled`
   - `client_reference_id=<userId>`
   - `metadata={ userId }`
5. Return the session's `url`.

Trial eligibility: Stripe enforces one-trial-per-customer automatically via the Customer's trial history when both paths go through the same `stripe_customer_id`.

### 5.2 `POST /billing/portal`

**Auth:** required.
**Returns:** `{ url: string }`

Requires a `stripe_customer_id` on the user — 404 if the user never checked out. Calls `stripe.billingPortal.Session.Create({ customer, return_url: <APP_URL>/billing })`.

### 5.3 `POST /billing/webhook`

**Auth:** public. Signature verified via `Stripe-Signature` header against `STRIPE_WEBHOOK_SECRET`.

Preamble (runs before event dispatch):
1. Read raw body.
2. Verify signature.
3. Check `event.livemode` matches configured `STRIPE_MODE`. Mismatch → 400 + log.
4. INSERT into `billing_events` (stripe_event_id, event_type, livemode, payload). On duplicate `stripe_event_id` conflict → return 200 OK immediately (idempotent re-delivery).

Dispatched events:

| Stripe event | Handler behavior |
|-------------|------------------|
| `checkout.session.completed` | Extract `subscription`, `customer` from session. Retrieve subscription from Stripe (authoritative). Call `applyStripeState(userId, sub)`. |
| `customer.subscription.created` | Retrieve sub (may be same as above; idempotent). `applyStripeState`. |
| `customer.subscription.updated` | Retrieve sub. `applyStripeState` — picks up status transitions, cancellation flag, period rollover, plan swap. |
| `customer.subscription.deleted` | Set local `status='canceled'`. |
| `customer.subscription.paused` | Set `status='paused'`, `paused_at=NOW()`. |
| `customer.subscription.resumed` | Set `status='active'`, `paused_at=NULL`. |
| `invoice.payment_failed` | Call `stripe.subscriptions.update(id, { pause_collection: { behavior: 'keep_as_draft' } })`. Stripe fires `subscription.paused` which mutates local state. |
| `invoice.payment_succeeded` | Log only. (Subscription.updated fires separately with new `current_period_end`.) |
| `charge.refunded` | Log only. No entitlement change. |
| any other | Log only (already written by preamble). |

`applyStripeState(userId, sub)` — one upsert resolving status, plan (from price ID), `current_period_end`, `trial_end`, `cancel_at_period_end` from the Stripe subscription object. Plan lookup: price ID → plan name via reverse map of the same env config used by checkout.

### 5.4 `POST /billing/refresh`

**Auth:** required.
**Returns:** same shape as `GET /billing/subscription`.

Calls `stripe.subscriptions.list({ customer: user.stripe_customer_id, status: 'all', limit: 1 })`. If a subscription exists, `applyStripeState`. If not, no-op. Logs `user_refresh_triggered` to `billing_events`. Rate-limited to 10/min per user (guard against abuse).

### 5.5 `GET /billing/subscription`

**Auth:** required.
**Returns:**
```json
{
  "status": "trialing|active|past_due|paused|canceled|comped|none",
  "plan": "pro_monthly|pro_annual|comp|null",
  "currentPeriodEnd": "2026-05-21T00:00:00Z|null",
  "trialEnd": "2026-04-28T00:00:00Z|null",
  "cancelAtPeriodEnd": false,
  "isActive": true
}
```

`status: "none"` for users with no `user_subscriptions` row.
`isActive` = the same boolean `user_has_ai_access()` would return.

### 5.6 `GET /billing/plans`

**Auth:** public.
**Returns:** `[{ plan: "pro_monthly", priceEur: 6.99, interval: "month" }, { plan: "pro_annual", priceEur: 59.99, interval: "year", discountPct: 29 }]`

Config-driven; lets us tune display prices without frontend redeploys.

### 5.7 Admin endpoints (env-gated)

- `POST /admin/comp-subscription` — body `{ userId, expiresAt: ISO-date|null, reason: string }`. Upserts row with `status='comped'`, `plan='comp'`, `current_period_end=expiresAt`, `stripe_customer_id=null`. Logs `admin_comp_granted` with `{reason, actor, expiresAt}` in payload.
- `DELETE /admin/comp-subscription` — body `{ userId, reason: string }`. Sets `status='canceled'`. Logs `admin_comp_revoked`.

Both require `ADMIN_API_ENABLED=true` at boot **and** an admin auth token (reuses Spec A's admin gate).

Spec A's `POST /admin/set-ai-subscription` is removed in this spec's migration.

## 6. Control Flow (Lifecycle Scenarios)

### 6.1 New trial signup (happy path)

```
User clicks "Start 7-day trial" in PaywallCard
  → POST /billing/checkout {plan: "pro_monthly"}
  → Backend: get-or-create stripe_customer_id
  → Backend: create Checkout Session (trial_period_days=7, automatic_tax=on)
  → return {url}
User → Stripe Checkout (card form) → submits → returns to /billing?status=success
Stripe → POST /billing/webhook [checkout.session.completed]
  → Backend: verify sig + livemode → insert billing_events
  → retrieve sub → applyStripeState(user, sub):
       status='trialing', plan='pro_monthly', trial_end=now+7d, current_period_end=now+7d
/billing fetches subscription → isActive=true → UI shows "Trial active, 7 days remaining"
AI pipeline: user_has_ai_access(userId) → true → unlocks
```

### 6.2 Trial → paid conversion (automatic)

```
Day 7: Stripe charges
  → webhook invoice.payment_succeeded (logged)
  → webhook customer.subscription.updated (status='active', current_period_end=now+30d)
  → applyStripeState upserts
User sees no difference except /billing now reads "Renews <date>"
```

### 6.3 Trial → paid conversion fails (card declined)

```
Day 7: charge fails
  → webhook invoice.payment_failed
  → Backend: stripe.subscriptions.update(subId, {pause_collection: {behavior: "keep_as_draft"}})
  → Stripe → webhook customer.subscription.paused → status='paused', paused_at=NOW()
  → user_has_ai_access → false
/billing shows red "Payment failed — update your card" banner with portal CTA
User → portal → updates card → resumes → webhook customer.subscription.resumed → status='active'
AI access restored next pipeline call
```

### 6.4 Mid-period cancellation

```
User → /billing → portal → "Cancel plan"
Stripe: cancel_at_period_end=true (sub stays active until period end)
  → webhook customer.subscription.updated → cancel_at_period_end=true
/billing shows orange "Ends <date>. Resubscribe anytime." banner
user_has_ai_access stays true until period end
Period end:
  → webhook customer.subscription.deleted → status='canceled'
  → user_has_ai_access → false; PaywallCard returns
```

### 6.5 Missed webhook recovery

```
Server outage / firewall / delivery lag: webhook for user U not applied
User: "I paid but can't generate" → clicks Refresh on /billing
  → POST /billing/refresh
  → stripe.subscriptions.list({customer: U.stripe_customer_id, limit: 1})
  → applyStripeState → local state now correct
  → return fresh /billing/subscription response → UI re-renders
```

Cron backstop (01:00 UTC, `billingReconcile.go`):
```
SELECT user_id, stripe_sub_id FROM user_subscriptions WHERE stripe_sub_id IS NOT NULL
For each: stripe.subscriptions.retrieve(stripe_sub_id)
  If state differs from local: applyStripeState + log cron_reconciled
Rate: 100 req/min to Stripe
Emit metric: reconciliations_performed, drifts_corrected
```

### 6.6 Admin comp (support / beta)

```
Admin → POST /admin/comp-subscription {userId: 42, expiresAt: "2026-12-31", reason: "Beta tester"}
  → upsert user_subscriptions {status:'comped', plan:'comp', current_period_end: 2026-12-31, stripe_customer_id:null}
  → billing_events(event_type='admin_comp_granted', payload={reason, actor, expiresAt})
user_has_ai_access(42) → true through 2026-12-31
```

### 6.7 Immediate access revocation (rare support case)

For the unusual case where a refund should also revoke access:
```
Admin via Stripe dashboard: "Cancel subscription immediately"
  → webhook customer.subscription.deleted → status='canceled'
  → access gone on next pipeline call
```

Default refund (issued without cancellation) does **not** revoke access — the user keeps Pro through `current_period_end`.

## 7. Error Handling

| Failure | Handling |
|--------|----------|
| Webhook signature invalid | 400 + structured log. Do not process. |
| Webhook livemode mismatch | 400 + structured log + alert. Do not process. |
| Webhook duplicate (`event.id` conflict) | 200 OK, short-circuit before dispatch. |
| Webhook handler panics mid-dispatch | 500. Stripe retries with exponential backoff (up to ~3 days). `billing_events` row already written → on retry, idempotency short-circuits only if insert succeeded. If panic was before insert, retry re-inserts normally. |
| Stripe API call fails (checkout create, portal create, subscription.list, subscription.retrieve) | Surface 502 with `{kind: "upstream_stripe", message}`. Do not mutate local state on failure. |
| `CreateCheckoutSession` for user already subscribed | 409 `{kind: "already_subscribed"}`. |
| `CreatePortalSession` for user without `stripe_customer_id` | 404 `{kind: "no_customer"}`. Frontend re-routes to `/pricing`. |
| `user_has_ai_access` called during outage of `user_subscriptions` reads | Bubble up error to pipeline; pipeline returns a distinguished `entitlement_unknown` which the handler maps to 503. Never default to true. |
| Cron job: Stripe rate-limit | Back off; resume next cycle. Metric `reconciliations_rate_limited` incremented. |
| Migration backfill: `users.ai_subscription_active` missing | Migration runs on a schema without Spec A's flag (clean install) → backfill no-op, continues. |

## 8. Frontend UX

### 8.1 Routes

- `/pricing` — public. Feature list, plan toggle (monthly / annual), per-plan price tile, "Start 7-day trial" CTA. Reachable from landing page, profile, QuotaBadge when free, AiCheckModal/AiGenerationControls paywall links.
- `/billing` — authed. Current plan status, renewal / trial-end date, "Manage subscription" portal link, "Refresh status" button, conditional banners.

### 8.2 Paywall entry points

1. **Inline** — existing `components/ai/PaywallCard.vue`. Now contains a two-tile toggle (monthly / annual) + CTA "Start 7-day trial." CTA → `POST /billing/checkout` → `window.location.href = url`.
2. **Pricing page** — long-form feature-by-feature layout with FAQ. Same checkout call.
3. **Landing page** — unauthenticated `/` renders existing hero + new "See pricing" link to `/pricing`.

### 8.3 `/billing` banners

Priority (top-to-bottom, one at most shown):
- Paused (payment failed): red. Copy: "Payment failed. Update your card to restore AI access." CTA: Manage subscription.
- Cancel at period end: orange. Copy: "Your Pro access ends on <date>. Resubscribe anytime." CTA: Manage subscription.
- Comped: neutral. Copy: "Complimentary access" + "expires <date>" or "no expiry."
- Trialing: blue. Copy: "Free trial — <N> days remaining. Converts to <plan> on <date>." CTA: Manage subscription.
- Active: green. Copy: "Pro — renews on <date>." CTA: Manage subscription.
- No subscription: gray. Copy: "You're on the free plan." CTA: See pricing → `/pricing`.

### 8.4 Post-checkout return

- `/billing?status=success&session_id=...` → toast "Welcome to Pro!" → `stores/billing.refresh()` once (picks up webhook-lagged state) → render banner.
- `/pricing?status=cancelled` → silent return; pricing re-displayed.

### 8.5 Pinia store (`stores/billing.ts`)

State:
```ts
{
  subscription: {
    status: 'none'|'trialing'|'active'|'past_due'|'paused'|'canceled'|'comped',
    plan: 'pro_monthly'|'pro_annual'|'comp'|null,
    currentPeriodEnd: string|null,
    trialEnd: string|null,
    cancelAtPeriodEnd: boolean,
    isActive: boolean,
  } | null,
  plans: Plan[] | null,
  loading: boolean,
  error: string | null,
}
```

Actions:
- `fetch()` — `GET /billing/subscription`; cached.
- `refresh()` — `POST /billing/refresh` then re-fetch.
- `checkout(plan)` — `POST /billing/checkout`, redirect.
- `portal()` — `POST /billing/portal`, redirect.
- `fetchPlans()` — `GET /billing/plans`.

Invalidation: re-fetch on login, on app resume (Capacitor `appStateChange`), after `refresh()`, after returning from Checkout with `?status=success`.

### 8.6 Navigation integration

- Profile → "Billing" row (authed users).
- Profile → "Upgrade to Pro" row (when not active).
- `components/ai/QuotaBadge.vue` — tapping opens `/billing` if active, `/pricing` otherwise.

## 9. Environment & Test-Mode Isolation

Required env at boot:
- `STRIPE_MODE` — `test` or `live`.
- `STRIPE_SECRET_KEY` — must start with `sk_test_` when `STRIPE_MODE=test`, `sk_live_` when `STRIPE_MODE=live`. Mismatch → refuse to boot.
- `STRIPE_WEBHOOK_SECRET` — separate secret per mode.
- `STRIPE_PRICE_PRO_MONTHLY`, `STRIPE_PRICE_PRO_ANNUAL` — must start with `price_`.
- `APP_URL` — used for `success_url` / `cancel_url` / portal `return_url`.
- `ADMIN_API_ENABLED` — same flag Spec A uses; gates `/admin/comp-subscription`.

Every webhook event checks `event.livemode === (STRIPE_MODE === 'live')`. Mismatch → 400 + structured alert log.

Reconciliation cron checks `STRIPE_MODE` before calling Stripe — never runs in test mode against a non-test key.

## 10. Testing

### Unit (backend)
- `applyStripeState`: all status transitions (active → paused, paused → active, active → cancel_at_period_end=true, cancel_at_period_end → canceled, trialing → active).
- Plan resolution: price ID → plan name; unknown price ID → `failed_to_map` logged, upsert skipped.
- Webhook idempotency: same `event.id` twice → one row in `billing_events`, one state change.
- Livemode mismatch: signature-valid event with wrong `livemode` → 400, no state change.
- Key-prefix assertion: boot with `STRIPE_MODE=live` and `sk_test_xxx` → boot fails with specific error.
- `user_has_ai_access`: returns true for `trialing/active/comped` within period, false for `paused/past_due/canceled`, false for expired comp.

### Integration (backend with DB + Stripe test mode)
- Full checkout → webhook → subscription row created flow (signed webhook delivered to test endpoint).
- Payment failure path: simulate `invoice.payment_failed` → verify `stripe.subscriptions.update` called with `pause_collection` → verify local status goes `paused` after `subscription.paused` event.
- Cancellation path: cancel via Stripe API → webhook chain → status transitions correctly.
- Reconciliation cron: manually desync local state → run cron → state corrected.
- Refresh endpoint: manually desync → call `/billing/refresh` → state corrected.
- Admin comp: POST → row with `status='comped'`, `stripe_customer_id=null`, `user_has_ai_access=true`. DELETE → `status='canceled'`, `user_has_ai_access=false`.
- Migration: schema with Spec A's flag + one user flagged `true` → run migration → one `comped` row exists, `ai_subscription_active` column gone, pipeline still reads entitlement correctly.

### Frontend (component)
- `PaywallCard.vue`: renders plan toggle; clicking CTA calls `stores/billing.checkout(plan)`.
- `PricingPage.vue`: renders both plans from `/billing/plans`; renders "See current plan" link when authed + active.
- `BillingPage.vue`: renders correct banner per status; Refresh button calls `refresh()`.
- Post-checkout return: `/billing?status=success` triggers `refresh()` once.

### Manual QA (end-to-end, Stripe test mode)
- New signup → 7-day trial → wait (simulate via Stripe CLI `trigger`) → auto-conversion → verify Pro active.
- New signup → trial → cancel mid-trial → verify keeps access until trial end → period end → verify access revoked.
- Subscribe → trigger `invoice.payment_failed` → verify paused UI → update card via portal → verify resumed UI.
- Admin comp → user gains Pro without payment.
- Cross-mode guard: deploy test webhook to live endpoint → live endpoint rejects with livemode mismatch.

## 11. Observability

- **Metrics:**
  - `billing_webhook_received_total{event_type, outcome}` — counter (outcome ∈ `applied`, `duplicate`, `mismatch`, `error`).
  - `billing_checkout_session_created_total{plan}` — counter.
  - `billing_portal_session_created_total` — counter.
  - `billing_refresh_triggered_total` — counter.
  - `billing_reconciliations_performed_total` — counter.
  - `billing_reconciliation_drifts_corrected_total` — counter.
  - `billing_livemode_mismatch_total` — counter, alert on >0.
- **Structured logs:** every state transition logs `{user_id, stripe_sub_id, from_status, to_status, from_plan, to_plan, source}` where `source ∈ webhook|cron|refresh|admin|migration`.
- **SQL probes (runbook):**
  - Current paying users: `SELECT COUNT(*) FROM user_subscriptions WHERE status IN ('active','trialing')`.
  - Revenue at risk: `SELECT COUNT(*) FROM user_subscriptions WHERE status = 'paused'`.
  - Recent drifts: `SELECT * FROM billing_events WHERE event_type='cron_reconciled' ORDER BY received_at DESC LIMIT 50`.

## 12. Out of Scope (Deferred)

- Mobile in-app purchases (Apple + Google) — **Spec C.1**.
- Multi-currency pricing / PPP adjustments — post-launch based on paying-user geography.
- Tiered plans (Pro / Pro+) — after usage data justifies.
- Quota top-ups / credit packs — requires consumption-based billing redesign.
- Student / educator discounts — requires verification partner.
- Family / group plans — requires access-sharing model.
- Public refund policy — requires support bandwidth.
- Referral rewards / promo codes (Stripe coupons usable via dashboard only for now).
- Abandoned-cart email campaigns.
- Dunning retry recovery — we intentionally don't retry (Q6 = C).
- Revenue analytics dashboards (Stripe's built-in dashboard covers v1 needs).

## 13. Open Questions (Non-Blocking)

- Launch prices (€6.99/€59.99) are starting guesses; tune before announcing based on comparable indie SaaS.
- Whether to add a "Claim beta comp" self-service page for approved beta testers (vs. admin endpoint only) — can be added without schema change.
- Webhook endpoint path: `/billing/webhook` vs. Stripe's convention `/webhooks/stripe` — leave as `/billing/webhook` for symmetry with the rest of the namespace unless ops has a preference.
