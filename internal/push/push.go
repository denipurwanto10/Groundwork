// Package push sends Web Push notifications (VAPID).
//
// The desktop notification of the app is raised by the service worker of the
// browser. While the tab stays open that is enough, but a closed tab is only
// reachable through the push service of the browser: the operator subscribes
// once, the server keeps that subscription, and every message is posted to
// it.
//
// The whole feature is open protocol: no third party service, no account
// anywhere else. The server owns a VAPID key pair of its own, the browser
// subscribes with the public half, and the private half signs every request
// to the push service.
//
// Without the keys the feature is off: the server keeps running, the
// endpoints answer `enabled: false`, and nothing is sent.
package push

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"entity-management/internal/auth"
	"entity-management/internal/db"
)

// SendTimeout bounds one delivery to the push service. A push service that
// does not answer in time must not hold the request that caused it.
const SendTimeout = 10 * time.Second

// ErrGone means the subscription is dead: the push service answered 404 or
// 410, so the browser threw it away and the row has to go with it.
var ErrGone = errors.New("push subscription is gone")

// ErrDelivery means the push service refused the message.
//
// The reason of the refusal is not written to the log: the error of the
// transport carries the endpoint of the browser, and that endpoint is a
// credential of its own.
var ErrDelivery = errors.New("the push service refused the message")

// Config holds the VAPID settings of the server. They come from the
// environment (VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY, VAPID_SUBJECT).
type Config struct {
	// PublicKey is handed to the browser so it can subscribe.
	PublicKey string
	// PrivateKey signs the requests to the push service. It never leaves
	// the server: not the log, not the API, not the repository.
	PrivateKey string
	// Subject says who the sender is. The push services want a mailto:
	// address or an https URL of the operator of the server.
	Subject string
}

// Enabled reports whether the server can send pushes.
//
// Both keys are needed: the public one is what the browser subscribes with,
// the private one signs the request. A server without them runs exactly as
// before — it simply never sends a push.
func (c Config) Enabled() bool {
	return c.PublicKey != "" && c.PrivateKey != ""
}

// Payload is the message the service worker expects: {title, body, tag, url}.
//
// The shape is fixed by frontend/public/sw-notify.js — the worker reads
// those four fields and nothing else.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	URL   string `json:"url"`
}

// Subscription is one browser that asked to be notified.
type Subscription struct {
	ID       int64
	UserID   int64
	Endpoint string
	P256dh   string
	Auth     string
	// UserAgent is the browser that registered, kept for the operator to
	// recognise which device a subscription belongs to.
	UserAgent string
}

// SubscriptionKeys are the two keys of a push subscription.
type SubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// SubscriptionInput is the body of POST /api/push/subscribe: the
// PushSubscription JSON the browser hands over.
type SubscriptionInput struct {
	Endpoint string           `json:"endpoint"`
	Keys     SubscriptionKeys `json:"keys"`
}

// FieldErrors maps a field name to the reason it was rejected. The handler
// turns it into the validation envelope the other endpoints use.
type FieldErrors map[string]string

// Error implements the error interface so a FieldErrors can be returned as one.
func (f FieldErrors) Error() string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+f[k])
	}
	return strings.Join(parts, "; ")
}

// Normalize trims the text of the body.
func (in *SubscriptionInput) Normalize() {
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Keys.P256dh = strings.TrimSpace(in.Keys.P256dh)
	in.Keys.Auth = strings.TrimSpace(in.Keys.Auth)
}

// Validate reports every rejected field at once. An empty map means the
// body is usable.
//
// The endpoint has to be an https URL: the push service of the browser is
// always reached over https, and a subscription that points somewhere else
// is not one. Both keys are required — without them the push service cannot
// decrypt anything.
func (in *SubscriptionInput) Validate() FieldErrors {
	errs := FieldErrors{}

	endpoint := strings.TrimSpace(in.Endpoint)
	switch {
	case endpoint == "":
		errs["endpoint"] = "endpoint is required"
	default:
		u, err := url.Parse(endpoint)
		switch {
		case err != nil:
			errs["endpoint"] = "endpoint must be a valid URL"
		case u.Scheme != "https":
			errs["endpoint"] = "endpoint must be an https URL"
		case u.Host == "":
			errs["endpoint"] = "endpoint must be a valid URL"
		}
	}

	if strings.TrimSpace(in.Keys.P256dh) == "" {
		errs["keys.p256dh"] = "keys.p256dh is required"
	}
	if strings.TrimSpace(in.Keys.Auth) == "" {
		errs["keys.auth"] = "keys.auth is required"
	}

	return errs
}

// Sender delivers one message to one subscription.
//
// The service talks to the push service of the browser through this
// interface, so the tests can use a fake one and never touch the network.
type Sender interface {
	Send(ctx context.Context, sub Subscription, payload Payload) error
}

// Service owns the subscriptions and hands the messages to the sender.
type Service struct {
	db     *db.Pool
	cfg    Config
	sender Sender
}

// NewService creates the push service.
//
// The sender may be nil: the service then only keeps the subscriptions and
// never sends anything.
func NewService(pool *db.Pool, cfg Config, sender Sender) *Service {
	return &Service{db: pool, cfg: cfg, sender: sender}
}

// Enabled reports whether the server is able to send pushes.
func (s *Service) Enabled() bool {
	return s.cfg.Enabled() && s.sender != nil
}

// PublicKey is the key the browser subscribes with. Empty when the feature
// is off: without a working pair of keys there is nothing to subscribe to.
func (s *Service) PublicKey() string {
	if !s.Enabled() {
		return ""
	}
	return s.cfg.PublicKey
}

// Subscribe stores the subscription of a browser under an account.
//
// The endpoint is the identity of a browser: a second call with the same
// endpoint refreshes the row instead of adding one. When the endpoint
// changes hands — the same browser, another account — the row follows the
// new owner.
func (s *Service) Subscribe(ctx context.Context, userID int64, in SubscriptionInput, userAgent string) error {
	return s.upsert(ctx, userID, in, strings.TrimSpace(userAgent))
}

// Unsubscribe removes the subscription of a browser.
//
// Only the account that owns the row may remove it: the endpoint of somebody
// else stays untouched.
func (s *Service) Unsubscribe(ctx context.Context, userID int64, endpoint string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`,
		userID, strings.TrimSpace(endpoint))
	return err
}

// NotifyUser sends the payload to every subscription of one account and
// returns how many deliveries succeeded.
//
// A feature that is off is not an error: nothing is sent and zero is
// returned.
func (s *Service) NotifyUser(ctx context.Context, userID int64, payload Payload) int {
	if !s.Enabled() {
		log.Print("push: skipped, the VAPID keys are not configured")
		return 0
	}

	subs, err := s.subscriptionsForUsers(ctx, []int64{userID})
	if err != nil {
		log.Printf("push: read the subscriptions of account %d failed: %v", userID, err)
		return 0
	}

	return s.sendAll(ctx, subs, payload)
}

// NotifyUsers sends the payload to the accounts given.
func (s *Service) NotifyUsers(ctx context.Context, userIDs []int64, payload Payload) int {
	if len(userIDs) == 0 {
		return 0
	}
	if !s.Enabled() {
		log.Print("push: skipped, the VAPID keys are not configured")
		return 0
	}

	subs, err := s.subscriptionsForUsers(ctx, userIDs)
	if err != nil {
		log.Printf("push: read the subscriptions failed: %v", err)
		return 0
	}

	return s.sendAll(ctx, subs, payload)
}

// NotifyOperators sends the payload to the accounts that watch the fleet:
// the admins and the staff.
//
// The account that caused the event is left out, so the driver who pressed
// "Start trip" is not told about their own departure.
func (s *Service) NotifyOperators(ctx context.Context, excludeUserID int64, payload Payload) int {
	if !s.Enabled() {
		log.Print("push: skipped, the VAPID keys are not configured")
		return 0
	}

	ids, err := s.OperatorRecipients(ctx, excludeUserID)
	if err != nil {
		log.Printf("push: read the operators failed: %v", err)
		return 0
	}

	return s.NotifyUsers(ctx, ids, payload)
}

// OperatorRecipients returns the accounts that receive the operational
// events: the admins and the staff.
//
// The account that caused the event is left out, so the driver who pressed
// "Start trip" is not told about their own departure. A zero id means
// "nobody to leave out".
func (s *Service) OperatorRecipients(ctx context.Context, excludeUserID int64) ([]int64, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id
          FROM users
         WHERE role::text = ANY($1)
           AND ($2 = 0 OR id <> $2)
         ORDER BY id`,
		[]string{auth.RoleAdmin, auth.RoleStaff}, excludeUserID)
	if err != nil {
		return nil, fmt.Errorf("query the operators: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan the operators: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the operators: %w", err)
	}

	return ids, nil
}

// sendAll delivers the payload to every subscription and returns how many
// deliveries succeeded.
//
// A subscription the push service no longer knows about is removed from the
// database. Every other failure is only logged: the push is a side effect
// of a request and must never break it.
func (s *Service) sendAll(ctx context.Context, subs []Subscription, payload Payload) int {
	sent := 0

	for _, sub := range subs {
		err := s.sender.Send(ctx, sub, payload)
		switch {
		case err == nil:
			sent++
		case errors.Is(err, ErrGone):
			log.Printf("push: the subscription %d is gone, removing it", sub.ID)
			if delErr := s.deleteSubscription(ctx, sub); delErr != nil {
				log.Printf("push: removing the subscription %d failed: %v", sub.ID, delErr)
			}
		default:
			// Only the id of the subscription is written to the log: the
			// endpoint and the keys are secrets of the browser.
			log.Printf("push: sending to the subscription %d failed: %v", sub.ID, err)
		}
	}

	return sent
}

// Background runs the job in its own goroutine with its own context.
//
// A push notification is a side effect of a request: it must never hold the
// request up, and it must never make it fail. The context of the job is not
// the context of the request — that one is closed as soon as the answer is
// written.
func Background(job func(ctx context.Context)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), SendTimeout)
		defer cancel()
		job(ctx)
	}()
}
