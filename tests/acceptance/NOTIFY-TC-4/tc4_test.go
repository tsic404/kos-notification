// Package notifytc4 verifies bounded retry and failure isolation (NOTIFY-AC-4,
// NOTIFY-TC-4): a channel that cannot be reached and a channel without a
// credential both leave failed records with a readable reason, their retries
// stop at a bound, the reachable channel of the same subscription still
// delivers, and the other subscription is not affected.
package notifytc4

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsic404/kos-notification/pkg/notification"
	"github.com/tsic404/kos-notification/tests/acceptance/harness"
)

const (
	channelSecret = "whsec-tc4-delivery-secret"
	maxRounds     = 20
)

// webhook records the deliveries one channel receives.
type webhook struct {
	mutex sync.Mutex
	hits  int
}

// ServeHTTP accepts one delivery.
func (w *webhook) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.hits++
	response.WriteHeader(http.StatusNoContent)
}

// Hits returns how many deliveries the channel received.
func (w *webhook) Hits() int {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.hits
}

// TestTC4BoundedRetryAndIsolation points one subscription at a reachable and an
// unreachable channel and another subscription at the unreachable channel and
// one without a credential, then drives the rounds until the retries stop.
func TestTC4BoundedRetryAndIsolation(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	now := createdAt.Add(10 * time.Minute)
	source := notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"}
	spec := notification.ChannelSpec{Kind: notification.ChannelKindWebhook}

	hooks := &webhook{}
	working := httptest.NewServer(hooks)
	defer working.Close()
	bare := &webhook{}
	unauthenticated := httptest.NewServer(bare)
	defer unauthenticated.Close()
	// An address nothing listens on: the request is refused, which is what an
	// unreachable channel looks like to the sender (NOTIFY-C-4).
	closed := httptest.NewServer(http.NotFoundHandler())
	unreachable := closed.URL
	closed.Close()

	controlPlane := harness.NewControlPlane()
	spec.Endpoint = working.URL
	controlPlane.AddChannel("hooks", spec, channelSecret)
	spec.Endpoint = unreachable
	controlPlane.AddChannel("broken", spec, channelSecret)
	spec.Endpoint = unauthenticated.URL
	controlPlane.AddChannel("bare", spec, "")
	controlPlane.AddSubscription("apps", notification.SubscriptionSpec{
		Source: source, Reasons: []string{"Failed"}, Channels: []string{"hooks", "broken"},
	}, createdAt)
	controlPlane.AddSubscription("audit", notification.SubscriptionSpec{
		Source: source, Reasons: []string{"Failed"}, Channels: []string{"broken", "bare"},
	}, createdAt)
	controlPlane.AddEvent(notification.Event{
		Name: "shop.17f0", Source: source, Reason: "Failed", LastSeen: now.Add(-time.Minute),
	})

	controller := controlPlane.Controller(now)
	rounds := 0
	for rounds < maxRounds {
		before := len(controlPlane.StoredRecords())
		if _, err := controller.Sync(ctx); err != nil {
			t.Fatalf("delivery round %d: %v", rounds+1, err)
		}
		rounds++
		if len(controlPlane.StoredRecords()) == before {
			break
		}
	}
	if rounds >= maxRounds {
		t.Fatalf("the failed channels still retry after %d rounds", rounds)
	}

	// The reachable channel of the same subscription delivered, despite the
	// unreachable channel in the same subscription.
	delivered := recordsOf(t, controlPlane, "hooks", "apps", source)
	if len(delivered) != 1 {
		t.Fatalf("the reachable channel has %d records, want 1", len(delivered))
	}
	if delivered[0].Spec.Result != notification.ResultDelivered || delivered[0].Spec.Reason != "" {
		t.Fatalf("the reachable channel's record reads %q/%q, want a delivery",
			delivered[0].Spec.Result, delivered[0].Spec.Reason)
	}

	// Every failing channel retried, then stopped with its last reason
	// readable, and each attempt is its own record.
	for name, records := range map[string][]notification.Record{
		"apps/broken":  recordsOf(t, controlPlane, "broken", "apps", source),
		"audit/broken": recordsOf(t, controlPlane, "broken", "audit", source),
		"audit/bare":   recordsOf(t, controlPlane, "bare", "audit", source),
	} {
		if len(records) < 2 {
			t.Errorf("%s made %d attempts; a failed delivery retries", name, len(records))
			continue
		}
		if attempts := attemptNumbers(records); !slices.Equal(attempts, sequence(len(records))) {
			t.Errorf("%s reads the attempts %v, want one record per attempt", name, attempts)
		}
		for _, record := range records {
			if record.Spec.Result != notification.ResultFailed {
				t.Errorf("%s record %d reads result %q", name, record.Spec.Attempt, record.Spec.Result)
			}
			if strings.TrimSpace(record.Spec.Reason) == "" {
				t.Errorf("%s record %d carries no failure reason", name, record.Spec.Attempt)
			}
		}
		terminal := terminalRecord(records)
		if terminal.Spec.Result != notification.ResultFailed || terminal.Spec.Reason == "" {
			t.Errorf("%s terminal record reads %q/%q, want a readable failure",
				name, terminal.Spec.Result, terminal.Spec.Reason)
		}
		if name == "audit/bare" && !strings.Contains(terminal.Spec.Reason, "凭据") {
			t.Errorf("%s failure reason does not name the missing credential: %q", name, terminal.Spec.Reason)
		}
	}

	// Retries stopped: further rounds add nothing, and the reachable channels
	// received no duplicate delivery.
	settled := len(controlPlane.StoredRecords())
	for range 3 {
		if _, err := controller.Sync(ctx); err != nil {
			t.Fatalf("replay a settled round: %v", err)
		}
	}
	if after := len(controlPlane.StoredRecords()); after != settled {
		t.Fatalf("settled deliveries retried %d more times", after-settled)
	}
	if hits := hooks.Hits(); hits != 1 {
		t.Fatalf("the reachable channel received %d deliveries, want 1", hits)
	}
	if hits := bare.Hits(); hits != 0 {
		t.Fatalf("a channel without a credential received %d deliveries", hits)
	}
}

// recordsOf reads the records of one target.
func recordsOf(t *testing.T, controlPlane *harness.ControlPlane, channel, subscription string, source notification.ObjectRef) []notification.Record {
	t.Helper()
	records, err := controlPlane.Records(context.Background(), notification.RecordQuery{
		Channel: channel, Subscription: subscription, Source: source,
	})
	if err != nil {
		t.Fatalf("read the delivery records: %v", err)
	}
	return records
}

// attemptNumbers reads the attempt numbers of one delivery's records in order.
func attemptNumbers(records []notification.Record) []int {
	attempts := make([]int, 0, len(records))
	for _, record := range records {
		attempts = append(attempts, record.Spec.Attempt)
	}
	slices.Sort(attempts)
	return attempts
}

// terminalRecord reads the record of the highest attempt.
func terminalRecord(records []notification.Record) notification.Record {
	terminal := notification.Record{}
	for _, record := range records {
		if record.Spec.Attempt > terminal.Spec.Attempt {
			terminal = record
		}
	}
	return terminal
}

// sequence returns the attempt numbers one delivery makes in order.
func sequence(count int) []int {
	attempts := make([]int, count)
	for index := range attempts {
		attempts[index] = index + 1
	}
	return attempts
}
