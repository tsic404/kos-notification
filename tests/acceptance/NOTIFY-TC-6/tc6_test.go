// Package notifytc6 verifies the read boundary and the retention NOTIFY-C-6
// declares: a record is read under the visibility of its source, so the Owner
// and the Member that owns the source read it while a Guest and an unrelated
// Member are refused with a reason rather than an empty set; a record outlives
// its deleted source and subscription and is marked so; disabling the component
// keeps the records and stops new ones; uninstalling keeps them and lists them
// as residuals (NOTIFY-AC-6, NOTIFY-TC-6).
package notifytc6

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tsic404/kos-notification/pkg/notification"
	"github.com/tsic404/kos-notification/tests/acceptance/harness"

	"github.com/tsic404/kos-sdk/manifest"
)

const reference = "oci://registry.kos.tsic.top/system/kos-notification:1.0.0"

// TestTC6RecordVisibilityAndRetention reads one source's record as the Owner,
// as the Members the source names or is owned by, and as a Guest, then deletes
// the source and the subscription, disables and re-enables the component, and
// uninstalls it.
func TestTC6RecordVisibilityAndRetention(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	now := createdAt.Add(10 * time.Minute)

	artifact, archive, err := notification.Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}
	cluster := harness.New()
	if err := cluster.Install(notification.Identifier, reference, archive, artifact.Digest.String()); err != nil {
		t.Fatalf("install the package: %v", err)
	}

	// The declaration retains the delivery records on uninstall, which is what
	// puts them in the residual list (NOTIFY-C-6, KOSD-C-29).
	declaration, err := cluster.Manifest(notification.Identifier)
	if err != nil {
		t.Fatalf("read the installed declaration: %v", err)
	}
	if !slices.ContainsFunc(declaration.Retention, func(entry manifest.Retention) bool {
		return entry.Resource == notification.KindDeliveryRecord && entry.Keep
	}) {
		t.Fatalf("the delivery record is not declared kept on uninstall: %+v", declaration.Retention)
	}

	// One source application naming two members, and one non-application object
	// owned by the first of them.
	app := notification.ObjectRef{Kind: notification.KindApplication, Namespace: "apps", Name: "shop"}
	volume := notification.ObjectRef{Kind: "Volume", Name: "data"}
	members := []string{"alice", "carol"}

	plane := harness.NewControlPlane()
	plane.Served = cluster.Serves
	plane.AddSource(app, notification.Source{Members: members})
	plane.AddSource(volume, notification.Source{Owner: "alice"})
	plane.AddChannel("hooks", notification.ChannelSpec{
		Kind: notification.ChannelKindWebhook, Endpoint: "https://hooks.example.test/kos",
	}, "")
	plane.AddSubscription("apps", notification.SubscriptionSpec{
		Source: app, Reasons: []string{"Failed"}, Channels: []string{"hooks"},
	}, createdAt)
	plane.AddSubscription("volumes", notification.SubscriptionSpec{
		Source: volume, Channels: []string{"hooks"},
	}, createdAt)
	plane.AddEvent(notification.Event{
		Name: "shop.17f0", Source: app, Reason: "Failed", LastSeen: now.Add(-time.Minute),
	})
	plane.SetObjectState(volume, []notification.Condition{{
		Type: "Reconciled", Status: "False", Reason: "VolumeLost",
		LastTransitionTime: now.Add(-5 * time.Minute),
	}})

	controller := plane.Controller(now)
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("delivery round: %v", err)
	}
	appRecords, err := plane.RecordsOf(ctx, app)
	if err != nil {
		t.Fatalf("read the application's records: %v", err)
	}
	volumeRecords, err := plane.RecordsOf(ctx, volume)
	if err != nil {
		t.Fatalf("read the volume's records: %v", err)
	}
	if len(appRecords) != 1 || len(volumeRecords) != 1 {
		t.Fatalf("the round wrote %d application and %d volume records, want one of each",
			len(appRecords), len(volumeRecords))
	}

	reader := notification.RecordReader{Records: plane, Sources: plane, Subscriptions: plane}
	owner := notification.Session{Username: "owner", Role: notification.RoleOwner}
	alice := notification.Session{Username: "alice", Role: notification.RoleMember}
	carol := notification.Session{Username: "carol", Role: notification.RoleMember}
	bob := notification.Session{Username: "bob", Role: notification.RoleMember}
	guest := notification.Session{Username: "guest", Role: notification.RoleGuest}

	// The Owner and the member the application names read the same record; the
	// answer carries the stored record and whether its source and subscription
	// still exist (NOTIFY-C-6, KOSD-C-59).
	for _, session := range []notification.Session{owner, alice, carol} {
		set, err := reader.Read(ctx, session, app)
		if err != nil {
			t.Fatalf("%s reading the application's record was refused: %v", session.Role, err)
		}
		if !set.SourceExists {
			t.Errorf("%s reads the record as a source that does not exist", session.Role)
		}
		if len(set.Records) != 1 || set.Records[0].Name != appRecords[0].Name {
			t.Fatalf("%s reads %+v, want the record %s", session.Role, set.Records, appRecords[0].Name)
		}
		if !set.Records[0].SubscriptionExists {
			t.Errorf("%s reads the record as a deleted subscription", session.Role)
		}
		if set.Records[0].Spec.Source != app {
			t.Errorf("%s reads the record's source as %s", session.Role, set.Records[0].Spec.Source)
		}
	}

	// A member the application does not name, and a guest the application does
	// not admit, are refused with a reason: the read is never an empty set
	// (NOTIFY-C-6, KOSD-C-59).
	for session, wanted := range map[notification.Session]string{
		bob:   "可见成员",
		guest: "访客可见",
	} {
		_, err := reader.Read(ctx, session, app)
		rejection := refusalOf(t, err)
		if rejection.Code != notification.CodeRecordNotVisible {
			t.Errorf("%s refusal code is %q, want %q", session.Role, rejection.Code, notification.CodeRecordNotVisible)
		}
		if !strings.Contains(rejection.Reason, wanted) {
			t.Errorf("%s refusal reason %q does not name the missing %q", session.Role, rejection.Reason, wanted)
		}
	}

	// An application that admits guests becomes readable to them.
	plane.AddSource(app, notification.Source{Members: members, AllowGuests: true})
	if _, err := reader.Read(ctx, guest, app); err != nil {
		t.Errorf("a guest of an application that admits guests was refused: %v", err)
	}
	plane.AddSource(app, notification.Source{Members: members})

	// A non-application source is readable to the Owner and to the member that
	// owns it only; the member the application names but does not own the
	// object is refused (NOTIFY-C-6, KOSD-C-64).
	for _, session := range []notification.Session{owner, alice} {
		set, err := reader.Read(ctx, session, volume)
		if err != nil {
			t.Fatalf("%s reading the volume's record was refused: %v", session.Role, err)
		}
		if len(set.Records) != 1 || set.Records[0].Name != volumeRecords[0].Name {
			t.Fatalf("%s reads %+v, want the record %s", session.Role, set.Records, volumeRecords[0].Name)
		}
	}
	for session, wanted := range map[notification.Session]string{
		carol: "不拥有",
		bob:   "不拥有",
		guest: "不是应用",
	} {
		_, err := reader.Read(ctx, session, volume)
		if rejection := refusalOf(t, err); !strings.Contains(rejection.Reason, wanted) {
			t.Errorf("%s refusal reason %q does not name %q", session.Role, rejection.Reason, wanted)
		}
	}

	// Deleting the source application succeeds while its records name it, and
	// the records stay readable and marked (NOTIFY-C-6).
	if err := plane.RemoveSource(app); err != nil {
		t.Fatalf("delete the source application: %v", err)
	}
	set, err := reader.Read(ctx, owner, app)
	if err != nil {
		t.Fatalf("read the records of a deleted source: %v", err)
	}
	if set.SourceExists {
		t.Fatalf("the read reports a deleted source as existing")
	}
	if len(set.Records) != 1 || set.Records[0].Name != appRecords[0].Name {
		t.Fatalf("the deleted source's records read as %+v, want %s", set.Records, appRecords[0].Name)
	}
	if set.Records[0].SourceExists {
		t.Fatalf("the record of a deleted source is not marked")
	}
	// A member or guest cannot be shown a source whose visibility no longer
	// exists.
	for _, session := range []notification.Session{alice, guest} {
		_, err := reader.Read(ctx, session, app)
		if rejection := refusalOf(t, err); !strings.Contains(rejection.Reason, "已不存在") {
			t.Errorf("%s refusal reason %q does not name the deleted source", session.Role, rejection.Reason)
		}
	}

	// Deleting the subscription keeps its records readable and marked, and the
	// deleted subscription owes no further delivery (NOTIFY-C-2, NOTIFY-C-6).
	plane.RemoveSubscription("volumes")
	set, err = reader.Read(ctx, owner, volume)
	if err != nil {
		t.Fatalf("read the records of a deleted subscription: %v", err)
	}
	if len(set.Records) != 1 || set.Records[0].Name != volumeRecords[0].Name {
		t.Fatalf("the deleted subscription's records read as %+v, want %s", set.Records, volumeRecords[0].Name)
	}
	if set.Records[0].SubscriptionExists {
		t.Fatalf("the record of a deleted subscription is not marked")
	}
	if !set.Records[0].SourceExists {
		t.Fatalf("the volume's record is marked as a deleted source")
	}
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("delivery round after deleting the subscription: %v", err)
	}
	if records, err := plane.RecordsOf(ctx, volume); err != nil || len(records) != 1 {
		t.Fatalf("the deleted subscription produced %d more records: %v", len(records)-1, err)
	}

	// Disabling the component keeps the records and stops new ones: the class is
	// no longer served, so a round writes nothing (NOTIFY-C-6, KOSD-C-28).
	stored := plane.StoredRecords()
	if err := cluster.Disable(notification.Identifier); err != nil {
		t.Fatalf("disable the component: %v", err)
	}
	if cluster.Serves(notification.KindDeliveryRecord) {
		t.Fatalf("a disabled component still serves the delivery record class")
	}
	report, err := controller.Sync(ctx)
	if err != nil {
		t.Fatalf("delivery round while disabled: %v", err)
	}
	if after := plane.StoredRecords(); len(after) != len(stored) {
		t.Fatalf("a disabled component wrote %d more records", len(after)-len(stored))
	}
	if !slices.ContainsFunc(report.Problems, func(problem string) bool {
		return strings.Contains(problem, "not served")
	}) {
		t.Errorf("the round reports %v, want the unserved class named", report.Problems)
	}

	// Re-enabling resumes delivery with the existing records unchanged.
	if err := cluster.Enable(notification.Identifier); err != nil {
		t.Fatalf("enable the component: %v", err)
	}
	if !cluster.Serves(notification.KindDeliveryRecord) {
		t.Fatalf("a re-enabled component does not serve the delivery record class")
	}
	if _, err := controller.Sync(ctx); err != nil {
		t.Fatalf("delivery round after enabling: %v", err)
	}
	resumed := plane.StoredRecords()
	if len(resumed) != len(stored)+1 {
		t.Fatalf("the re-enabled component wrote %d records, want 1", len(resumed)-len(stored))
	}
	if !slices.EqualFunc(resumed[:len(stored)], stored, sameRecord) {
		t.Fatalf("the existing records changed while the component was disabled")
	}

	// Uninstalling keeps the records and lists every one of them as a residual
	// (NOTIFY-C-6, KOSD-C-29).
	for _, record := range resumed {
		cluster.Seed(notification.KindDeliveryRecord, recordObject(t, record))
	}
	residuals, err := cluster.Uninstall(notification.Identifier)
	if err != nil {
		t.Fatalf("uninstall the component: %v", err)
	}
	if cluster.Serves(notification.KindDeliveryRecord) {
		t.Fatalf("an uninstalled component still serves the delivery record class")
	}
	kept := make([]string, 0, len(residuals))
	for _, residual := range residuals {
		if residual.Kind != notification.KindDeliveryRecord {
			t.Errorf("the residual list carries a %s, want the delivery records", residual.Kind)
			continue
		}
		name, _ := residual.Object["name"].(string)
		kept = append(kept, name)
	}
	wanted := make([]string, 0, len(resumed))
	for _, record := range resumed {
		wanted = append(wanted, record.Name)
	}
	if !slices.Equal(kept, wanted) {
		t.Fatalf("the residual list carries %v, want the delivery records %v", kept, wanted)
	}
}

// refusalOf reads a refusal and fails the case for any other error, so a read
// answered with records or with an empty set cannot pass for a refusal.
func refusalOf(t *testing.T, err error) *notification.Rejection {
	t.Helper()
	if err == nil {
		t.Fatalf("the read was answered without a refusal")
	}
	rejection := &notification.Rejection{}
	if !errors.As(err, &rejection) {
		t.Fatalf("the read was not refused with a reason: %v", err)
	}
	if strings.TrimSpace(rejection.Reason) == "" {
		t.Fatalf("the refusal carries no reason")
	}
	return rejection
}

// sameRecord reports whether two reads carry the same stored record.
func sameRecord(left, right notification.Record) bool {
	return left.Name == right.Name && left.Spec == right.Spec
}

// recordObject renders one record as the object a residual list carries.
func recordObject(t *testing.T, record notification.Record) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode the record %s: %v", record.Name, err)
	}
	object := map[string]any{}
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("decode the record %s: %v", record.Name, err)
	}
	return object
}
