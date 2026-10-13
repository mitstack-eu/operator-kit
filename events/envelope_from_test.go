package events_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/mitstack-eu/operator-kit/v2/events"
)

func TestEnvelopeFrom_ReturnsTheEnvelopeWithEnvelopeStashed(t *testing.T) {
	want := events.Envelope{
		SubscriptionID: "sub-1",
		Operation:      "subscribe",
		IdempotencyKey: "key-1",
		Callback:       &events.Callback{URL: "https://api.mitstack.dev/v1/webhooks/operator-events", Token: "evt-token"},
	}
	ctx := events.WithEnvelope(context.Background(), want)

	got, ok := events.EnvelopeFrom(ctx)
	if !ok {
		t.Fatal("EnvelopeFrom found no envelope on a context WithEnvelope stashed one on")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnvelopeFrom = %+v, want %+v", got, want)
	}
}

func TestEnvelopeFrom_ReportsAnEnvelopeWithNoCallbackAsFoundWithANilCallback(t *testing.T) {
	ctx := events.WithEnvelope(context.Background(), events.Envelope{SubscriptionID: "sub-1"})

	got, ok := events.EnvelopeFrom(ctx)
	if !ok {
		t.Fatal("EnvelopeFrom found no envelope, want the stashed one even though it carries no callback")
	}
	if got.Callback != nil {
		t.Errorf("Callback = %+v, want nil: the stashed envelope carries none", got.Callback)
	}
	if got.SubscriptionID != "sub-1" {
		t.Errorf("SubscriptionID = %q, want sub-1", got.SubscriptionID)
	}
}

func TestEnvelopeFrom_ReportsNotFoundOnAContextWithNoEnvelope(t *testing.T) {
	got, ok := events.EnvelopeFrom(context.Background())
	if ok {
		t.Errorf("EnvelopeFrom = %+v, true on a bare context, want false", got)
	}
}
