# Spec C — Subscription Billing (Outline) — SUPERSEDED

**Status:** Superseded by full spec [`2026-04-21-subscription-billing-design.md`](2026-04-21-subscription-billing-design.md) (web / Stripe scope only). Mobile IAP will be Spec C.1.
**Date:** 2026-04-19
**Scope:** Replace the `ai_subscription_active` admin-flip stub introduced by Spec A with real payment rails. Web via Stripe Checkout/Portal; mobile via Apple App Store and Google Play in-app purchases (Capacitor). Covers sign-up, renewal, cancellation, grace period, and receipt validation.

Not in scope: enterprise/team plans, gift cards, promo codes beyond Stripe coupons, referral rewards, student discounts (unless decided otherwise).

---

## Purpose

StudBud is a freemium app. The free tier gives unlimited manual flashcard authoring, training, and reactive-mode home. The paid tier unlocks AI features (generation, check, revision plans, and future AI work). This spec answers: how do users pay? How does the backend know they paid? What happens when they stop paying?

## Dependencies

- **Spec A (shipped)** — exposes `ai_subscription_active` on users. This spec's migration flips the writer from the admin endpoint to the billing webhook.
- **Spec A (shipped)** — exposes per-feature daily quotas (`prompt`, `pdf`, `check`). Spec B adds `plan`. Tiers in this spec define the daily caps per feature.

## Known Decisions (carved out from prior specs)

- `ai_subscription_active` is the single source of truth for "AI access allowed." Tier-specific limits are enforced by `aiQuotaService` daily counters. (Spec A §2)
- Admin-flip endpoint (`POST /admin/set-ai-subscription`) stays as a dev/QA backdoor but is guarded by `ADMIN_API_ENABLED=true`. (Spec A §3.1)
- Entitlement check lives inside the AI pipeline, never in handlers. Nothing in this spec changes where the check happens — only what flips the flag. (Spec A §3.2)

## Key Open Questions (resolve during brainstorming)

### Product
1. **Tier shape** — single paid tier, or tiered (e.g. Pro / Pro+)? If tiered, what splits the tiers (quota caps vs feature gates)?
2. **Pricing** — monthly, annual, lifetime? What currency breadth (EUR + USD + GBP only, or auto-localized)?
3. **Free trial** — length (7d? 14d?), card-required or not, one-per-user enforcement.
4. **Cancellation UX** — downgrade at period end (Netflix-style) or immediate? Does cancelling mid-period keep access for the paid days?
5. **Student discount** — yes/no? If yes, verification via SheerID or honor-system?
6. **Family / group plan** — deferred to v2 or in-scope now?
7. **Refund policy** — user-visible policy + how refunds map to entitlement revocation.

### Technical
8. **Receipt validation** — Stripe webhook for web is straightforward; for IAP, backend calls Apple's `verifyReceipt` / Google's Play Developer API. Which backend service owns this?
9. **Platform parity** — is an Apple subscription valid on web and Android too? Or is "one subscription per platform" acceptable (industry norm)? If cross-platform, store mapping on the user.
10. **Proration on upgrade** — if tiered, how do we handle Pro → Pro+ mid-period?
11. **Restore purchases** — iOS/Android flow; web users need a "link account" path.
12. **Chargebacks / disputes** — grace period or immediate revocation?
13. **Subscription pausing** (Stripe feature) — supported or not?
14. **Test mode isolation** — separate Stripe/Apple/Google sandbox users must not flip prod flags. Guard on env.

### Regulatory / compliance
15. **VAT / sales tax** — Stripe Tax? Apple/Google handle for mobile. For web, which stack?
16. **SCA (Strong Customer Authentication)** — Stripe handles; confirm the Checkout flow we pick supports it by default.
17. **GDPR data** — what payment-related data lands in our DB vs stays with the processor?
18. **Receipt emails** — processor-sent or our own templates?

## Architectural Sketch (non-binding)

```
┌─────────────┐      ┌──────────────────────┐      ┌──────────────────┐
│   Client    │──────│ /billing/checkout    │─────▶│   Stripe / IAP   │
│  (web/mob)  │      │ /billing/portal      │      │     processor    │
└─────────────┘      └──────────────────────┘      └──────────────────┘
                                                           │
                                                           │ webhook / verify
                                                           ▼
                                                   ┌──────────────────┐
                                                   │ billingService   │
                                                   │  - verifies      │
                                                   │  - updates user  │
                                                   └──────────────────┘
                                                           │
                                                           ▼
                                                   ┌──────────────────┐
                                                   │ users.ai_subscr… │
                                                   │ users.plan_tier  │
                                                   │ billing_events[] │
                                                   └──────────────────┘
```

### New modules (tentative)
- `api/service/billingService.go` — webhook handlers, receipt verification, entitlement writer.
- `api/handler/billingHandler.go` — `POST /billing/checkout`, `POST /billing/portal`, `POST /billing/verify-iap`, `POST /billing/restore`.
- `pkg/billing/` — provider abstraction (`StripeProvider`, `AppleIAPProvider`, `GooglePlayProvider` behind a common interface).
- `api/migrations/` — `billing_events` event-sourced audit table (every webhook / verify call logged), `users.plan_tier`, `users.current_period_end`.

### Frontend
- `pages/PricingPage.vue` — tier presentation, CTA to checkout.
- `pages/BillingPortalPage.vue` — links to Stripe Customer Portal on web; in-app IAP management on mobile.
- `components/paywall/PaywallCard.vue` — already exists from Spec A as a placeholder; reuse and wire to Pricing.
- Capacitor plugin choice: `revenuecat` wraps Apple + Google uniformly and also syncs to webhooks. Alternative: `@capacitor-community/in-app-purchases` (direct, no third-party). Open question.

## Risks

- **IAP rules** — Apple forbids linking to web checkout from iOS. If we want cross-platform sub (mobile pays once, valid on web), architecture and UX copy need care to stay compliant.
- **Refund drift** — if we revoke entitlement on Stripe refund but the user has training state tied to "plan generated today," what happens? Probably no action needed (plan is derived data), but worth confirming.
- **Sandbox contamination** — Stripe test keys and production webhooks must never share secrets. Env-gate hard.
- **Time zones** — daily quota resets at user's local midnight (Spec A). Subscription "period end" is UTC. Don't conflate.

## Testing Strategy (high-level)

- Unit: entitlement-flip idempotency (same webhook twice → one state change).
- Integration: mocked Stripe webhook → user row updated → pipeline allows AI call.
- Manual: full Stripe Checkout sandbox flow, Apple sandbox subscriber, Google test track subscriber, restore on a fresh install.
- Regression: admin-flip still works when `ADMIN_API_ENABLED=true`.

## Next Step Before Full Spec

Brainstorming session to answer the product questions above (tiers, pricing, trial, cross-platform parity). Once those are locked, the technical spec is straightforward — mostly boilerplate around webhook handling and receipt verification.
