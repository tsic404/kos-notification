// Package notifytc3 verifies the configuration surface NOTIFY-C-1 and
// NOTIFY-C-7 declare: the channel types the create entry lists are the types
// the write accepts, an unsupported type is rejected with its reason, a
// subscription's target channel set is readable, and the page's three items
// carry a source and the right modification entry or read-only reason. Disabling
// the component withdraws the page and the classes and keeps the records
// (NOTIFY-AC-3, NOTIFY-TC-3).
package notifytc3

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/tsic404/kos-notification/pkg/notification"
	"github.com/tsic404/kos-notification/tests/acceptance/harness"
)

const (
	reference      = "oci://registry.kos.tsic.top/system/kos-notification:1.0.0"
	webhookAddress = "https://hooks.example.test/kos"
)

// objectRecorder records the extension objects a write path stores.
type objectRecorder struct {
	written []notification.Object
}

// Put records one object write.
func (r *objectRecorder) Put(_ context.Context, object notification.Object) error {
	r.written = append(r.written, object)
	return nil
}

// TestTC3ConfigurationSurface installs the package and checks the create entry,
// the write acceptance, the subscription channel set and the page items, then
// disables the component and reads the page and the classes again.
func TestTC3ConfigurationSurface(t *testing.T) {
	artifact, archive, err := notification.Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}
	cluster := harness.New()
	if err := cluster.Install(notification.Identifier, reference, archive, artifact.Digest.String()); err != nil {
		t.Fatalf("install the package: %v", err)
	}

	// The create entry lists the types the write accepts: one list feeds both.
	listed := listedChannelKinds(t)
	if !slices.Equal(listed, notification.SupportedChannelKinds()) {
		t.Fatalf("create entry lists %v, the write accepts %v", listed, notification.SupportedChannelKinds())
	}
	for _, kind := range listed {
		if err := (notification.ChannelSpec{Kind: kind, Endpoint: webhookAddress}).Validate(); err != nil {
			t.Fatalf("a listed channel type %q is not accepted: %v", kind, err)
		}
	}

	objects := &objectRecorder{}
	writers := notification.Writers{Objects: objects, Credentials: notification.NewMemoryCredentials()}
	owner := notification.Session{Username: "owner", Role: notification.RoleOwner}

	// An unsupported type is rejected with its reason and writes nothing.
	_, err = writers.WriteChannel(context.Background(), owner, notification.ChannelWrite{
		Name: "mail",
		Spec: notification.ChannelSpec{Kind: "email", Endpoint: webhookAddress},
	})
	rejection := &notification.Rejection{}
	if !errors.As(err, &rejection) {
		t.Fatalf("an unsupported channel type was not rejected: %v", err)
	}
	if rejection.Code != notification.CodeUnsupportedChannelKind {
		t.Fatalf("rejection code is %q, want %q", rejection.Code, notification.CodeUnsupportedChannelKind)
	}
	if !strings.Contains(rejection.Reason, "不支持的渠道类型") {
		t.Fatalf("rejection carries no reason: %q", rejection.Reason)
	}
	if len(objects.written) != 0 {
		t.Fatalf("a rejected channel write stored %d objects", len(objects.written))
	}

	channel, err := writers.WriteChannel(context.Background(), owner, notification.ChannelWrite{
		Name: "hooks",
		Spec: notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: webhookAddress},
	})
	if err != nil {
		t.Fatalf("write a supported channel: %v", err)
	}
	if channel.CredentialSet {
		t.Fatalf("a channel written without a credential reports one set")
	}

	// A subscription's target channel set is readable, one subscription may
	// name several channels, and an empty set is rejected with its reason.
	subscription, err := writers.WriteSubscription(context.Background(), owner, notification.SubscriptionWrite{
		Name: "apps",
		Spec: notification.SubscriptionSpec{
			Source:   notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"},
			Reasons:  []string{"Failed"},
			Channels: []string{"hooks", "audit"},
		},
	})
	if err != nil {
		t.Fatalf("write a subscription: %v", err)
	}
	if !slices.Equal(subscription.Channels, []string{"hooks", "audit"}) {
		t.Fatalf("subscription target channels read as %v", subscription.Channels)
	}
	written := len(objects.written)
	_, err = writers.WriteSubscription(context.Background(), owner, notification.SubscriptionWrite{
		Name: "empty",
		Spec: notification.SubscriptionSpec{Source: notification.ObjectRef{Kind: "Application", Name: "shop"}},
	})
	if !errors.As(err, &rejection) || rejection.Code != notification.CodeInvalidSubscription {
		t.Fatalf("a subscription without a channel was not rejected: %v", err)
	}
	if !strings.Contains(rejection.Reason, "至少指向一个渠道") {
		t.Fatalf("rejection carries no reason: %q", rejection.Reason)
	}
	if len(objects.written) != written {
		t.Fatalf("the rejected subscription wrote an object: %d objects stored", len(objects.written)-written)
	}

	// The page carries the three items with their sources, and each item has
	// either its modification entry or the read-only reason.
	page := notificationPage(t, cluster.Pages())
	if page.ID != notification.PageNotification {
		t.Fatalf("page id is %q", page.ID)
	}
	if len(page.Items) != 3 {
		t.Fatalf("page declares %d items, want 3", len(page.Items))
	}
	actions := map[string]notification.Action{}
	for _, action := range page.Actions {
		actions[action.ID] = action
	}
	items := map[string]notification.PageItem{}
	for _, item := range page.Items {
		items[item.ID] = item
		if item.Source != notification.SourceObject {
			t.Errorf("item %q source is %q, want %q", item.ID, item.Source, notification.SourceObject)
		}
		if want := notification.Identifier + "/" + item.ID; item.Reference != want {
			t.Errorf("item %q reference is %q, want %q", item.ID, item.Reference, want)
		}
	}
	for id, expected := range map[string]struct {
		action string
		kind   string
	}{
		notification.ItemChannels:      {notification.ActionEditChannel, notification.KindChannel},
		notification.ItemSubscriptions: {notification.ActionEditSubscription, notification.KindSubscription},
	} {
		item, ok := items[id]
		if !ok {
			t.Fatalf("page item %q is missing", id)
		}
		if item.Action != expected.action {
			t.Errorf("item %q action is %q, want %q", id, item.Action, expected.action)
		}
		if item.Reason != "" {
			t.Errorf("item %q carries a modification entry and a reason", id)
		}
		action, ok := actions[expected.action]
		if !ok {
			t.Errorf("item %q names an undeclared action %q", id, expected.action)
			continue
		}
		if action.RequiredRole != string(notification.RoleOwner) {
			t.Errorf("action %q admits %q, want the Owner only", expected.action, action.RequiredRole)
		}
		if route := classRoute(expected.kind); !strings.HasPrefix(action.Path, route) {
			t.Errorf("action %q path %q is not the %s class route %q", expected.action, action.Path, expected.kind, route)
		}
	}
	records, ok := items[notification.ItemDeliveryRecords]
	if !ok {
		t.Fatalf("the delivery record item is missing")
	}
	if records.Action != "" {
		t.Errorf("the delivery record item offers a modification entry %q", records.Action)
	}
	if records.Reason != notification.ReasonRecordsReadOnly {
		t.Errorf("the delivery record item reason is %q", records.Reason)
	}

	// Every item's value comes from the object projection.
	reporting := notification.Reporting{
		Channels:      []notification.Channel{channel},
		Subscriptions: []notification.Subscription{subscription},
		Records: []notification.Record{{
			Name: "record-1",
			Spec: notification.RecordSpec{
				Channel: "hooks", Subscription: "apps",
				Source:  notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"},
				Attempt: 1, Result: notification.ResultFailed, Reason: "channel answered 500",
			},
		}},
	}
	for _, item := range page.Items {
		value, ok := reporting.Read(item.ID)
		if !ok {
			t.Errorf("item %q has no reader", item.ID)
			continue
		}
		if value == nil {
			t.Errorf("item %q reads no value", item.ID)
		}
	}

	// An empty projection is a current value, not an undeclared item: every
	// declared item reads as an empty list (NOTIFY-C-7, KOSD-C-53).
	empty := notification.Reporting{}
	for _, item := range page.Items {
		value, ok := empty.Read(item.ID)
		if !ok {
			t.Errorf("item %q is undeclared once the projection is empty", item.ID)
			continue
		}
		if encoded, err := json.Marshal(value); err != nil || string(encoded) != "[]" {
			t.Errorf("item %q reads %s from an empty projection, want []", item.ID, encoded)
		}
	}
	if _, ok := empty.Read("undeclared"); ok {
		t.Errorf("an item this product does not declare reads as declared")
	}

	// Disabling the component withdraws the page and the object classes and
	// keeps the records (KOSD-C-28, NOTIFY-C-3).
	cluster.Seed(notification.KindDeliveryRecord, map[string]any{
		"kind": notification.KindDeliveryRecord, "name": "record-1",
	})
	if err := cluster.Disable(notification.Identifier); err != nil {
		t.Fatalf("disable the component: %v", err)
	}
	if pages := cluster.Pages(); len(pages) != 0 {
		t.Fatalf("a disabled component still serves %d pages", len(pages))
	}
	if classes := cluster.ObjectClasses(); len(classes) != 0 {
		t.Fatalf("a disabled component still serves %d object classes", len(classes))
	}
	if records := cluster.Objects(notification.KindDeliveryRecord); len(records) != 1 {
		t.Fatalf("disabling the component kept %d delivery records, want 1", len(records))
	}
	if err := cluster.Enable(notification.Identifier); err != nil {
		t.Fatalf("enable the component: %v", err)
	}
	if pages := cluster.Pages(); len(pages) != 1 {
		t.Fatalf("a re-enabled component serves %d pages, want 1", len(pages))
	}
}

// classRoute is the control-plane route of one declared class (KOSD-C-12).
func classRoute(kind string) string {
	return "/api/v1/resources/" + kind + "?apiVersion=" + notification.APIGroup + "/" + notification.ObjectVersion
}

// listedChannelKinds reads the channel type list the configuration schema
// declares for the create entry.
func listedChannelKinds(t *testing.T) []string {
	t.Helper()
	manifest := notification.Manifest()
	config, ok := manifest["config"].(map[string]any)
	if !ok {
		t.Fatalf("the declaration carries no config block")
	}
	schema, ok := config["schema"].(map[string]any)
	if !ok {
		t.Fatalf("the config block carries no schema")
	}
	channels, ok := schema["channels"].(map[string]any)
	if !ok {
		t.Fatalf("the schema declares no channel structure")
	}
	properties, ok := channels["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the channel structure declares no properties")
	}
	kind, ok := properties["kind"].(map[string]any)
	if !ok {
		t.Fatalf("the channel structure declares no kind")
	}
	values, ok := kind["enum"].([]any)
	if !ok {
		t.Fatalf("the channel kind carries no type list")
	}
	kinds := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("the channel type list carries a non-string entry")
		}
		kinds = append(kinds, text)
	}
	return kinds
}

// notificationPage reads the installed notification page through the product's
// own page shape.
func notificationPage(t *testing.T, pages []map[string]any) notification.Page {
	t.Helper()
	if len(pages) != 1 {
		t.Fatalf("the component serves %d pages, want 1", len(pages))
	}
	encoded, err := json.Marshal(pages[0])
	if err != nil {
		t.Fatalf("encode the page: %v", err)
	}
	page := notification.Page{}
	if err := json.Unmarshal(encoded, &page); err != nil {
		t.Fatalf("decode the page: %v", err)
	}
	return page
}
