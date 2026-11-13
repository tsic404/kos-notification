// Package notifytc1 verifies delivery and records (NOTIFY-AC-1, NOTIFY-TC-1): a
// matching event delivered to a configured channel leaves one record per
// attempt with every declared field readable, the record set exposes the
// attempt count and the terminal result, and no user may modify or delete a
// record.
package notifytc1

import (
	"context"
	"errors"
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
	channelName   = "hooks"
	channelSecret = "whsec-tc1-delivery-secret"
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

// TestTC1DeliveryAndRecord configures one channel and one subscription,
// triggers two matching events and reads the delivery records back, then tries
// to modify and delete one.
func TestTC1DeliveryAndRecord(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	now := createdAt.Add(10 * time.Minute)
	source := notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"}

	hooks := &webhook{}
	server := httptest.NewServer(hooks)
	defer server.Close()

	controlPlane := harness.NewControlPlane()
	controlPlane.AddChannel(channelName,
		notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: server.URL}, channelSecret)
	controlPlane.AddSubscription("apps", notification.SubscriptionSpec{
		Source:   source,
		Reasons:  []string{"Failed"},
		Channels: []string{channelName},
	}, createdAt)
	controlPlane.AddEvent(notification.Event{
		Name: "shop.17f0", Source: source, Reason: "Failed", LastSeen: now.Add(-time.Minute),
	})

	controller := controlPlane.Controller(now)
	report, err := controller.Sync(ctx)
	if err != nil {
		t.Fatalf("deliver the matching event: %v", err)
	}
	if report.Attempts != 1 || report.Delivered != 1 || report.Failed != 0 {
		t.Fatalf("the round reported %+v, want one delivered attempt", report)
	}
	if hits := hooks.Hits(); hits != 1 {
		t.Fatalf("the channel received %d deliveries, want 1", hits)
	}

	// The attempt left one record carrying every field the contract names.
	records := recordsOf(t, controlPlane, channelName, "apps", source)
	if len(records) != 1 {
		t.Fatalf("the channel has %d records after one attempt, want 1", len(records))
	}
	record := records[0]
	if record.Name == "" {
		t.Fatalf("the record carries no name")
	}
	if record.Spec.Channel != channelName {
		t.Errorf("the record's channel is %q, want %q", record.Spec.Channel, channelName)
	}
	if record.Spec.Subscription != "apps" {
		t.Errorf("the record's subscription is %q, want %q", record.Spec.Subscription, "apps")
	}
	if record.Spec.Source != source {
		t.Errorf("the record's source is %+v, want %+v", record.Spec.Source, source)
	}
	if record.Spec.Attempt != 1 {
		t.Errorf("the first attempt's record is numbered %d", record.Spec.Attempt)
	}
	if record.Spec.Result != notification.ResultDelivered {
		t.Errorf("the record's result is %q, want %q", record.Spec.Result, notification.ResultDelivered)
	}
	if record.Spec.Reason != "" {
		t.Errorf("a delivered record carries the failure reason %q", record.Spec.Reason)
	}
	if attempts := attemptNumbers(records); !slices.Equal(attempts, []int{1}) {
		t.Errorf("the record set reads the attempts %v, want [1]", attempts)
	}
	if terminal := terminalRecord(records); terminal.Spec.Result != notification.ResultDelivered {
		t.Errorf("the record set reads the terminal result %q, want %q",
			terminal.Spec.Result, notification.ResultDelivered)
	}

	// A second matching event is a second delivery: its own attempt numbering
	// starts over, and replaying a round adds no attempt (NOTIFY-C-2).
	controlPlane.AddEvent(notification.Event{
		Name: "shop.17f1", Source: source, Reason: "Failed", LastSeen: now.Add(-30 * time.Second),
	})
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("deliver the second matching event: %v", err)
	}
	if hits := hooks.Hits(); hits != 2 {
		t.Fatalf("the channel received %d deliveries after two matches, want 2", hits)
	}
	records = recordsOf(t, controlPlane, channelName, "apps", source)
	if len(records) != 2 {
		t.Fatalf("the two deliveries left %d records, want one per delivery", len(records))
	}
	if records[0].Name == records[1].Name {
		t.Fatalf("the two deliveries share the record name %q", records[0].Name)
	}
	for _, record := range records {
		if record.Spec.Attempt != 1 || record.Spec.Result != notification.ResultDelivered {
			t.Errorf("record %s reads attempt %d result %q", record.Name, record.Spec.Attempt, record.Spec.Result)
		}
	}
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("replay the round: %v", err)
	}
	if len(recordsOf(t, controlPlane, channelName, "apps", source)) != 2 {
		t.Fatalf("a replayed round added a record")
	}
	if hits := hooks.Hits(); hits != 2 {
		t.Fatalf("a replayed round delivered again: %d deliveries", hits)
	}

	// No user role writes the delivery record class, so a modification and a
	// deletion through the control-plane API are refused (NOTIFY-C-3).
	for _, role := range []notification.Role{notification.RoleOwner, notification.RoleMember, notification.RoleGuest} {
		err := notification.AllowWrite(notification.KindDeliveryRecord, role)
		rejection := &notification.Rejection{}
		if !errors.As(err, &rejection) || rejection.Code != notification.CodeForbidden {
			t.Errorf("%s could modify or delete a delivery record: %v", role, err)
			continue
		}
		if !strings.Contains(rejection.Reason, "投递控制器") {
			t.Errorf("%s rejection does not name the controller-only writer: %q", role, rejection.Reason)
		}
	}
	if writers := notification.DeclaredObjectRoles()[notification.KindDeliveryRecord]; len(writers) != 0 {
		t.Fatalf("the record class declares the user writers %v", writers)
	}
	for _, entry := range notification.WriteEntries() {
		if strings.Contains(entry.Path, notification.KindDeliveryRecord) ||
			strings.Contains(strings.ToLower(entry.Path), notification.ResourceDeliveryRecord) {
			t.Errorf("the page offers the user write entry %q on delivery records", entry.ID)
		}
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
