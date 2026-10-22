package push

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fakeSender records what it was asked to deliver. The tests never touch a
// real push service.
type fakeSender struct {
	sent  []Payload
	fails error
}

func (f *fakeSender) Send(ctx context.Context, sub Subscription, payload Payload) error {
	f.sent = append(f.sent, payload)
	return f.fails
}

// The body of a subscribe is only accepted when it names an https endpoint
// and carries both keys of the subscription.
func TestSubscriptionInputValidate(t *testing.T) {
	cases := map[string]struct {
		in       SubscriptionInput
		wantKeys []string
	}{
		"an empty endpoint": {
			in:       SubscriptionInput{Keys: SubscriptionKeys{P256dh: "p", Auth: "a"}},
			wantKeys: []string{"endpoint"},
		},
		"an endpoint that is not https": {
			in: SubscriptionInput{
				Endpoint: "http://push.example.com/send/1",
				Keys:     SubscriptionKeys{P256dh: "p", Auth: "a"},
			},
			wantKeys: []string{"endpoint"},
		},
		"an endpoint that is not a URL": {
			in: SubscriptionInput{
				Endpoint: "not a url",
				Keys:     SubscriptionKeys{P256dh: "p", Auth: "a"},
			},
			wantKeys: []string{"endpoint"},
		},
		"an endpoint without a host": {
			in: SubscriptionInput{
				Endpoint: "https://",
				Keys:     SubscriptionKeys{P256dh: "p", Auth: "a"},
			},
			wantKeys: []string{"endpoint"},
		},
		"the public key of the subscription is missing": {
			in: SubscriptionInput{
				Endpoint: "https://push.example.com/send/1",
				Keys:     SubscriptionKeys{Auth: "a"},
			},
			wantKeys: []string{"keys.p256dh"},
		},
		"the secret of the subscription is missing": {
			in: SubscriptionInput{
				Endpoint: "https://push.example.com/send/1",
				Keys:     SubscriptionKeys{P256dh: "p"},
			},
			wantKeys: []string{"keys.auth"},
		},
		"a body that is empty": {
			in:       SubscriptionInput{},
			wantKeys: []string{"endpoint", "keys.p256dh", "keys.auth"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := tc.in
			in.Normalize()
			errs := in.Validate()

			if len(errs) != len(tc.wantKeys) {
				t.Fatalf("the rejected fields are %v, want %v", errs, tc.wantKeys)
			}
			for _, key := range tc.wantKeys {
				if _, ok := errs[key]; !ok {
					t.Errorf("the field %q was not rejected: %v", key, errs)
				}
			}
		})
	}
}

// A body that is complete is accepted.
func TestSubscriptionInputAcceptsAWholeSubscription(t *testing.T) {
	in := SubscriptionInput{
		Endpoint: "  https://push.example.com/send/abc  ",
		Keys:     SubscriptionKeys{P256dh: " p256 ", Auth: " auth "},
	}
	in.Normalize()

	if errs := in.Validate(); len(errs) != 0 {
		t.Fatalf("a whole subscription was rejected: %v", errs)
	}
	if in.Endpoint != "https://push.example.com/send/abc" {
		t.Errorf("the endpoint is %q, want the trimmed one", in.Endpoint)
	}
	if in.Keys.P256dh != "p256" || in.Keys.Auth != "auth" {
		t.Errorf("the keys are %q and %q, want the trimmed ones", in.Keys.P256dh, in.Keys.Auth)
	}
}

// The payload is exactly the shape the service worker reads: title, body,
// tag and url — and nothing else.
func TestPayloadJSONShape(t *testing.T) {
	payload := Payload{
		Title: "Unit berangkat",
		Body:  "TRK-001 berangkat menuju Gudang Cisitu",
		Tag:   "route-started-32",
		URL:   "/",
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode the payload: %v", err)
	}

	want := `{"title":"Unit berangkat","body":"TRK-001 berangkat menuju Gudang Cisitu","tag":"route-started-32","url":"/"}`
	if string(encoded) != want {
		t.Errorf("the payload is\n%s\nwant\n%s", encoded, want)
	}
}

// The keys of the payload are always written, even when they are empty:
// the worker reads them the same way every time.
func TestPayloadJSONKeepsEveryField(t *testing.T) {
	encoded, err := json.Marshal(Payload{})
	if err != nil {
		t.Fatalf("encode the payload: %v", err)
	}

	want := `{"title":"","body":"","tag":"","url":""}`
	if string(encoded) != want {
		t.Errorf("the empty payload is %s, want %s", encoded, want)
	}
}

// Without the keys the feature is off: nothing is sent, and sending is not
// an error either.
func TestEmptyConfigurationTurnsTheFeatureOff(t *testing.T) {
	sender := &fakeSender{}
	service := NewService(nil, Config{}, sender)

	if service.Enabled() {
		t.Error("the feature is on without the VAPID keys")
	}
	if service.PublicKey() != "" {
		t.Errorf("the public key is %q, want an empty one", service.PublicKey())
	}

	sent := service.NotifyUser(context.Background(), 1, Payload{Title: "Unit berangkat"})
	if sent != 0 {
		t.Errorf("%d messages were sent, want none", sent)
	}
	if len(sender.sent) != 0 {
		t.Errorf("the sender was called %d times, want none", len(sender.sent))
	}
}

// A config with only one of the two keys is not a working pair.
func TestHalfConfiguredKeysTurnTheFeatureOff(t *testing.T) {
	if (Config{PublicKey: "public"}).Enabled() {
		t.Error("the feature is on with the private key missing")
	}
	if (Config{PrivateKey: "private"}).Enabled() {
		t.Error("the feature is on with the public key missing")
	}
	if !(Config{PublicKey: "public", PrivateKey: "private"}).Enabled() {
		t.Error("the feature is off with a whole pair of keys")
	}
}

// A service without a sender keeps the subscriptions but sends nothing.
func TestServiceWithoutASenderSendsNothing(t *testing.T) {
	service := NewService(nil, Config{PublicKey: "public", PrivateKey: "private"}, nil)

	if service.Enabled() {
		t.Error("the feature is on without a sender")
	}
	if sent := service.NotifyUser(context.Background(), 1, Payload{}); sent != 0 {
		t.Errorf("%d messages were sent, want none", sent)
	}
}

// The subject of the VAPID token is a mailto: address or an https URL. The
// library adds the prefix itself, so it must not be there twice.
func TestVapidSubject(t *testing.T) {
	cases := map[string]string{
		"mailto:admin@localhost":      "admin@localhost",
		"admin@localhost":             "admin@localhost",
		"https://ops.example/contact": "https://ops.example/contact",
		"  ":                          "admin@localhost",
	}

	for in, want := range cases {
		if got := vapidSubject(in); got != want {
			t.Errorf("the subject of %q is %q, want %q", in, got, want)
		}
	}
}

// A delivery that fails is counted as a failure, not as a delivery.
func TestSendAllCountsOnlyTheDeliveriesThatWorked(t *testing.T) {
	sender := &fakeSender{fails: errors.New("the push service refused the message")}
	service := NewService(nil, Config{PublicKey: "p", PrivateKey: "s"}, sender)

	subs := []Subscription{{ID: 1, UserID: 1}, {ID: 2, UserID: 2}}
	if sent := service.sendAll(context.Background(), subs, Payload{Title: "x"}); sent != 0 {
		t.Errorf("%d messages were counted as sent, want none", sent)
	}
	if len(sender.sent) != 2 {
		t.Errorf("the sender was called %d times, want 2", len(sender.sent))
	}
}
