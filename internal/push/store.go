package push

import (
	"context"
	"fmt"
)

// The queries of the subscriptions.
//
// The endpoint is the identity of a browser, so it is the key of the upsert:
// a browser that subscribes twice keeps one row, and a browser that changes
// hands moves its row to the new account.

// upsert stores the subscription of a browser.
//
// The endpoint is the identity of a browser: a second call with the same
// endpoint refreshes the row instead of adding one. When the endpoint
// changes hands — the same browser, another account — the row follows the
// new owner.
func (s *Service) upsert(ctx context.Context, userID int64, in SubscriptionInput, userAgent string) error {
	agent := any(nil)
	if userAgent != "" {
		agent = userAgent
	}

	_, err := s.db.Exec(ctx, `
        INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (endpoint) DO UPDATE
           SET user_id      = EXCLUDED.user_id,
               p256dh       = EXCLUDED.p256dh,
               auth         = EXCLUDED.auth,
               user_agent   = EXCLUDED.user_agent,
               last_seen_at = now()`,
		userID, in.Endpoint, in.Keys.P256dh, in.Keys.Auth, agent)
	if err != nil {
		return fmt.Errorf("store the subscription: %w", err)
	}

	return nil
}

// subscriptionsForUsers returns the subscriptions of the accounts given.
func (s *Service) subscriptionsForUsers(ctx context.Context, userIDs []int64) ([]Subscription, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}

	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, endpoint, p256dh, auth, COALESCE(user_agent, '')
          FROM push_subscriptions
         WHERE user_id = ANY($1)
         ORDER BY id`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("query the subscriptions: %w", err)
	}
	defer rows.Close()

	subs := make([]Subscription, 0, len(userIDs))
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.ID, &sub.UserID, &sub.Endpoint,
			&sub.P256dh, &sub.Auth, &sub.UserAgent); err != nil {
			return nil, fmt.Errorf("scan the subscriptions: %w", err)
		}
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the subscriptions: %w", err)
	}

	return subs, nil
}

// SubscriptionsForUser returns the subscriptions of one account.
func (s *Service) SubscriptionsForUser(ctx context.Context, userID int64) ([]Subscription, error) {
	return s.subscriptionsForUsers(ctx, []int64{userID})
}

// deleteSubscription removes a row the push service no longer knows.
func (s *Service) deleteSubscription(ctx context.Context, sub Subscription) error {
	_, err := s.db.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1`, sub.ID)
	if err != nil {
		return fmt.Errorf("delete the subscription %d: %w", sub.ID, err)
	}
	return nil
}
