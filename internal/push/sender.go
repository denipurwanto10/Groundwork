package push

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// webpushSender delivers the messages through the push service of the
// browser (Mozilla, Google, Apple), signed with the VAPID keys of the
// server.
type webpushSender struct {
	cfg  Config
	http *http.Client
}

// NewSender creates the real sender: it talks to the push service of the
// browser over plain HTTPS.
//
// It is the one place in the project that leaves the machine. Everything
// else — the subscriptions, the recipients, the payloads — stays inside.
func NewSender(cfg Config) Sender {
	return &webpushSender{
		cfg: cfg,
		http: &http.Client{
			Timeout: SendTimeout,
		},
	}
}

// Send delivers one message to one subscription.
//
// A subscription the push service does not know anymore comes back as
// ErrGone, so the caller can remove the row.
func (s *webpushSender) Send(ctx context.Context, sub Subscription, payload Payload) error {
	message, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode the push message: %w", err)
	}

	options := &webpush.Options{
		VAPIDPublicKey:  s.cfg.PublicKey,
		VAPIDPrivateKey: s.cfg.PrivateKey,
		Subscriber:      vapidSubject(s.cfg.Subject),
		// Five minutes is long enough for a unit that is on its way and
		// short enough that a message nobody reads does not linger.
		TTL: int(pushTTL.Seconds()),
	}

	resp, err := webpush.SendNotificationWithContext(ctx, message, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}, options)
	if err != nil {
		// The error of the transport carries the endpoint of the browser,
		// and that endpoint is a credential of its own: it stays out of the
		// log.
		return ErrDelivery
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return nil
	case http.StatusNotFound, http.StatusGone:
		return fmt.Errorf("%w: the push service answered %d", ErrGone, resp.StatusCode)
	default:
		return fmt.Errorf("%w: the push service answered %d", ErrDelivery, resp.StatusCode)
	}
}

// vapidSubject prepares the "sub" claim of the VAPID token.
//
// The push services want a mailto: address or an https URL. The library adds
// the "mailto:" prefix itself when the subject is not an https URL, so the
// prefix is taken off here — otherwise "mailto:admin@localhost" would become
// "mailto:mailto:admin@localhost".
func vapidSubject(raw string) string {
	subject := strings.TrimSpace(raw)
	if subject == "" {
		return "admin@localhost"
	}
	return strings.TrimPrefix(subject, "mailto:")
}

// pushTTL is how long the push service keeps a message it could not deliver
// right away.
const pushTTL = 5 * time.Minute
