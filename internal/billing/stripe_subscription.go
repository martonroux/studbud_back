package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	stripe "github.com/stripe/stripe-go/v76"
	stripesubscription "github.com/stripe/stripe-go/v76/subscription"
)

// RetrieveSubscription fetches a single Stripe subscription by ID and
// returns a provider-agnostic Subscription snapshot.
func (c *StripeClient) RetrieveSubscription(ctx context.Context, subID string) (*Subscription, error) {
	s, err := stripesubscription.Get(subID, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe retrieve subscription:\n%w", err)
	}
	return projectSubscription(s), nil
}

// ListSubscriptionsByCustomer returns up to limit active subscriptions for
// the given Stripe customer ID, most recently created first.
func (c *StripeClient) ListSubscriptionsByCustomer(ctx context.Context, customerID string, limit int) ([]Subscription, error) {
	params := &stripe.SubscriptionListParams{
		Customer: stripe.String(customerID),
	}
	params.Limit = stripe.Int64(int64(limit))

	iter := stripesubscription.List(params)
	var subs []Subscription
	var pageEnds map[string]int64
	var pageList *stripe.SubscriptionList
	for iter.Next() {
		s := iter.Subscription()

		// stripe-go v76's list iterator only ever calls SetLastResponse on
		// the page container (see itemLevelCurrentPeriodEndsByID), never on
		// the individual *stripe.Subscription values it yields, so
		// projectSubscription's per-subscription raw-JSON fallback can't see
		// item-level current_period_end here. Rebuild the page-level lookup
		// whenever the container changes (i.e. a new page was fetched).
		if list := iter.SubscriptionList(); list != pageList {
			pageList = list
			pageEnds = itemLevelCurrentPeriodEndsByID(list)
		}

		sub := projectSubscription(s)
		if sub.CurrentPeriodEnd == nil {
			if end, ok := pageEnds[s.ID]; ok && end != 0 {
				t := time.Unix(end, 0)
				sub.CurrentPeriodEnd = &t
			}
		}
		subs = append(subs, *sub)
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("stripe list subscriptions:\n%w", err)
	}
	return subs, nil
}

// projectSubscription converts a stripe.Subscription to the local Subscription type.
func projectSubscription(s *stripe.Subscription) *Subscription {
	sub := &Subscription{
		ID:                s.ID,
		Status:            string(s.Status),
		CancelAtPeriodEnd: s.CancelAtPeriodEnd,
		Livemode:          s.Livemode,
	}

	if s.Customer != nil {
		sub.CustomerID = s.Customer.ID
	}

	if s.Items != nil && len(s.Items.Data) > 0 && s.Items.Data[0].Price != nil {
		sub.PriceID = s.Items.Data[0].Price.ID
	}

	currentPeriodEnd := s.CurrentPeriodEnd
	if currentPeriodEnd == 0 {
		currentPeriodEnd = itemLevelCurrentPeriodEnd(s)
	}
	if currentPeriodEnd != 0 {
		t := time.Unix(currentPeriodEnd, 0)
		sub.CurrentPeriodEnd = &t
	}

	if s.TrialEnd != 0 {
		t := time.Unix(s.TrialEnd, 0)
		sub.TrialEnd = &t
	}

	if s.PauseCollection != nil {
		t := time.Unix(s.Created, 0)
		sub.PausedAt = &t
	}

	return sub
}

// itemLevelCurrentPeriodEnd recovers current_period_end from the first
// subscription item when Stripe returns it there instead of on the
// top-level subscription object. Some account billing configurations do
// this, and stripe-go v76's typed SubscriptionItem has no field for it, so
// this re-parses the raw API response body stripe-go stashes on
// LastResponse. Returns 0 (absent) if the raw body is unavailable or the
// field isn't present at either level.
func itemLevelCurrentPeriodEnd(s *stripe.Subscription) int64 {
	if s.LastResponse == nil || len(s.LastResponse.RawJSON) == 0 {
		return 0
	}

	var raw struct {
		Items struct {
			Data []struct {
				CurrentPeriodEnd int64 `json:"current_period_end"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(s.LastResponse.RawJSON, &raw); err != nil {
		return 0
	}
	if len(raw.Items.Data) == 0 {
		return 0
	}
	return raw.Items.Data[0].CurrentPeriodEnd
}

// itemLevelCurrentPeriodEndsByID extracts item-level current_period_end for
// every subscription on a list page, keyed by subscription ID. Confirmed
// against stripe-go v76's source (stripe.go's Do): for a list call, Call/Do
// invokes SetLastResponse exactly once, on the *stripe.SubscriptionList
// container passed to CallRaw — never on the individual *stripe.Subscription
// values decoded into its Data slice. So those values' own LastResponse is
// always nil, and itemLevelCurrentPeriodEnd's fallback never fires for them.
// This re-parses the container's raw JSON instead, which does carry it.
func itemLevelCurrentPeriodEndsByID(list *stripe.SubscriptionList) map[string]int64 {
	ends := make(map[string]int64)
	if list == nil || list.LastResponse == nil || len(list.LastResponse.RawJSON) == 0 {
		return ends
	}

	var raw struct {
		Data []struct {
			ID    string `json:"id"`
			Items struct {
				Data []struct {
					CurrentPeriodEnd int64 `json:"current_period_end"`
				} `json:"data"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.LastResponse.RawJSON, &raw); err != nil {
		return ends
	}
	for _, item := range raw.Data {
		if len(item.Items.Data) > 0 {
			ends[item.ID] = item.Items.Data[0].CurrentPeriodEnd
		}
	}
	return ends
}
