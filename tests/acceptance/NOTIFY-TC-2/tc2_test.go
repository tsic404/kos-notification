// Package notifytc2 verifies matching and transition dedup (NOTIFY-AC-2,
// NOTIFY-TC-2): a matching event delivers and a non-matching one does not, an
// event from before the subscription was established is not backfilled, a
// state transition delivers once and the same state reconciled again does not,
// and a deleted subscription accumulates no new record.
package notifytc2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tsic404/kos-notification/pkg/notification"
	"github.com/tsic404/kos-notification/tests/acceptance/harness"
)

const (
	reference     = "oci://registry.kos.tsic.top/system/kos-notification:1.0.0"
	channelSecret = "whsec-tc2-delivery-secret"
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

// TestTC2MatchingAndTransitionDedup establishes one subscription, feeds the
// controller events inside and outside the window, then one source object's
// transitions, and finally reads the records back.
func TestTC2MatchingAndTransitionDedup(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 11, 13, 10, 10, 0, 0, time.UTC)
	now := createdAt.Add(10 * time.Minute)
	source := notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"}
	other := notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "blog"}

	hooks := &webhook{}
	server := httptest.NewServer(hooks)
	defer server.Close()

	controlPlane := harness.NewControlPlane()
	controlPlane.AddChannel("hooks",
		notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: server.URL}, channelSecret)
	controlPlane.AddSubscription("apps", notification.SubscriptionSpec{
		Source: source, Reasons: []string{"Failed"}, Channels: []string{"hooks"},
	}, createdAt)

	// One event is older than the subscription, one is older than the read
	// window, one matches, one has another reason and one another source.
	controlPlane.AddEvent(notification.Event{
		Name: "before-subscription", Source: source, Reason: "Failed", LastSeen: createdAt.Add(-5 * time.Minute),
	})
	controlPlane.AddEvent(notification.Event{
		Name: "outside-window", Source: source, Reason: "Failed", LastSeen: now.Add(-2 * time.Hour),
	})
	controlPlane.AddEvent(notification.Event{
		Name: "matching", Source: source, Reason: "Failed", LastSeen: now.Add(-5 * time.Minute),
	})
	controlPlane.AddEvent(notification.Event{
		Name: "other-reason", Source: source, Reason: "Pulled", LastSeen: now.Add(-4 * time.Minute),
	})
	controlPlane.AddEvent(notification.Event{
		Name: "other-source", Source: other, Reason: "Failed", LastSeen: now.Add(-3 * time.Minute),
	})

	controller := controlPlane.Controller(now)
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("deliver the matching event: %v", err)
	}
	if hits := hooks.Hits(); hits != 1 {
		t.Fatalf("the channel received %d deliveries, want only the matching event", hits)
	}
	if records := storedRecords(t, controlPlane, source); len(records) != 1 {
		t.Fatalf("the matching event left %d records, want 1", len(records))
	}

	// A condition that appears after the subscription is established triggers
	// once; reconciling the same observed state again adds nothing.
	controlPlane.SetObjectState(source, []notification.Condition{{
		Type: "Ready", Status: "True", Reason: "Succeeded", LastTransitionTime: now.Add(-2 * time.Minute),
	}})
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("deliver the state transition: %v", err)
	}
	if records := storedRecords(t, controlPlane, source); len(records) != 2 {
		t.Fatalf("the state transition left %d records in total, want 2", len(records))
	}
	for range 2 {
		if _, err := controller.Sync(ctx); err != nil {
			t.Fatalf("reconcile the same state: %v", err)
		}
	}
	if records := storedRecords(t, controlPlane, source); len(records) != 2 {
		t.Fatalf("reconciling the same state added records: %d in total", len(records))
	}
	if hits := hooks.Hits(); hits != 2 {
		t.Fatalf("reconciling the same state delivered again: %d deliveries", hits)
	}

	// A condition that changes value triggers once more; a transition back to
	// a previous value is a value change too.
	controlPlane.SetObjectState(source, []notification.Condition{{
		Type: "Ready", Status: "False", Reason: "Failed", LastTransitionTime: now.Add(-time.Minute),
	}})
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("deliver the changed condition: %v", err)
	}
	controlPlane.SetObjectState(source, []notification.Condition{{
		Type: "Ready", Status: "True", Reason: "Succeeded", LastTransitionTime: now,
	}})
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("deliver the condition transitioned back: %v", err)
	}
	if records := storedRecords(t, controlPlane, source); len(records) != 4 {
		t.Fatalf("the two value changes left %d records in total, want 4", len(records))
	}

	// A deleted subscription owes nothing new, whatever the events and states
	// that follow (NOTIFY-C-2).
	controlPlane.RemoveSubscription("apps")
	controlPlane.AddEvent(notification.Event{
		Name: "after-deletion", Source: source, Reason: "Failed", LastSeen: now.Add(-30 * time.Second),
	})
	controlPlane.SetObjectState(source, []notification.Condition{{
		Type: "Ready", Status: "Unknown", Reason: "Unknown", LastTransitionTime: now.Add(30 * time.Second),
	}})
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("run a round after the subscription was deleted: %v", err)
	}
	if records := storedRecords(t, controlPlane, source); len(records) != 4 {
		t.Fatalf("a deleted subscription produced %d new records", len(records)-4)
	}

	// Disabling the component withdraws the controller, so no round runs and no
	// new delivery starts (KOSD-C-28).
	artifact, archive, err := notification.Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}
	cluster := harness.New()
	if err := cluster.Install(notification.Identifier, reference, archive, artifact.Digest.String()); err != nil {
		t.Fatalf("install the package: %v", err)
	}
	if err := cluster.Disable(notification.Identifier); err != nil {
		t.Fatalf("disable the component: %v", err)
	}
	if scopes := cluster.Surface().ControllerScope; len(scopes) != 0 {
		t.Fatalf("a disabled component still contributes its delivery controller: %v", scopes)
	}
}

// storedRecords reads the records of one source object.
func storedRecords(t *testing.T, controlPlane *harness.ControlPlane, source notification.ObjectRef) []notification.Record {
	t.Helper()
	records, err := controlPlane.Records(context.Background(), notification.RecordQuery{
		Channel: "hooks", Subscription: "apps", Source: source,
	})
	if err != nil {
		t.Fatalf("read the delivery records: %v", err)
	}
	return records
}
