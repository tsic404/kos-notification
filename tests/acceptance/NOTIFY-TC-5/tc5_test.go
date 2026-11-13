// Package notifytc5 verifies the credential boundary NOTIFY-C-5 and KOSD-C-14
// declare: the channel configuration is the only write entry, a read returns
// only whether a credential is set, a non-Owner write is refused with its
// reason, and no declared surface carries the credential value (NOTIFY-AC-5,
// NOTIFY-TC-5).
package notifytc5

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/tsic404/kos-notification/pkg/notification"
)

const (
	channelAddress = "https://hooks.example.test/kos"
	channelSecret  = "whsec-9f2c4d7a-8e41-4b9c-9d2e-6a7b8c9d0e1f"
	blockedSecret  = "whsec-blocked-write-must-not-land"
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

// failingObjects fails every object write.
type failingObjects struct{ err error }

// Put fails the write.
func (f failingObjects) Put(context.Context, notification.Object) error { return f.err }

// auditRecorder collects the audit entries the write path produces.
type auditRecorder struct {
	entries []notification.AuditEntry
}

// Record collects one audit entry.
func (r *auditRecorder) Record(entry notification.AuditEntry) { r.entries = append(r.entries, entry) }

// TestTC5CredentialBoundary writes a channel with a credential and then reads
// every declared surface back.
func TestTC5CredentialBoundary(t *testing.T) {
	objects := &objectRecorder{}
	credentials := notification.NewMemoryCredentials()
	audit := &auditRecorder{}
	var logs bytes.Buffer
	writers := notification.Writers{
		Objects:     objects,
		Credentials: credentials,
		Audit:       audit,
		Log:         slog.New(slog.NewTextHandler(&logs, nil)),
	}
	owner := notification.Session{Username: "owner", Role: notification.RoleOwner}
	ctx := context.Background()

	channel, err := writers.WriteChannel(ctx, owner, notification.ChannelWrite{
		Name:       "hooks",
		Spec:       notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: channelAddress},
		Credential: channelSecret,
	})
	if err != nil {
		t.Fatalf("write the channel: %v", err)
	}

	// The positive case really registered the credential; the read returns only
	// whether it is set (NOTIFY-C-5).
	configured, err := credentials.Configured(ctx, "hooks")
	if err != nil || !configured {
		t.Fatalf("the credential was not registered: configured=%v err=%v", configured, err)
	}
	if !channel.CredentialSet {
		t.Fatalf("the channel read reports no credential after one was written")
	}
	assertReadSurface(t)

	// A non-Owner write is refused with its reason and leaves no side effect.
	for _, role := range []notification.Role{notification.RoleMember, notification.RoleGuest} {
		written := len(objects.written)
		_, err := writers.WriteChannel(ctx, notification.Session{Username: "member", Role: role}, notification.ChannelWrite{
			Name:       "blocked",
			Spec:       notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: channelAddress},
			Credential: blockedSecret,
		})
		rejection := &notification.Rejection{}
		if !errors.As(err, &rejection) {
			t.Fatalf("%s write was not refused: %v", role, err)
		}
		if rejection.Code != notification.CodeForbidden {
			t.Fatalf("%s write rejection code is %q, want %q", role, rejection.Code, notification.CodeForbidden)
		}
		if !strings.Contains(rejection.Reason, string(notification.KindChannel)) {
			t.Fatalf("%s write rejection names no class: %q", role, rejection.Reason)
		}
		if len(objects.written) != written {
			t.Fatalf("%s write stored %d objects", role, len(objects.written)-written)
		}
		if configured, _ := credentials.Configured(ctx, "blocked"); configured {
			t.Fatalf("%s write registered a credential", role)
		}
	}

	// The credential and the channel object commit or fail together: a failed
	// object write leaves no registration behind (NOTIFY-C-5, KOSD-C-12).
	unavailable := errors.New("object store unavailable")
	failing := notification.Writers{Objects: failingObjects{err: unavailable}, Credentials: credentials}
	if _, err := failing.WriteChannel(ctx, owner, notification.ChannelWrite{
		Name:       "hooks",
		Spec:       notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: channelAddress},
		Credential: "whsec-rotation",
	}); !errors.Is(err, unavailable) {
		t.Fatalf("a channel write whose object store failed returned %v", err)
	}
	if configured, err := credentials.Configured(ctx, "hooks"); err != nil || !configured {
		t.Fatalf("a failed channel write dropped the existing credential: configured=%v err=%v", configured, err)
	}
	if _, err := failing.WriteChannel(ctx, owner, notification.ChannelWrite{
		Name:       "audit",
		Spec:       notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: channelAddress},
		Credential: "whsec-audit",
	}); !errors.Is(err, unavailable) {
		t.Fatalf("a first channel write whose object store failed returned %v", err)
	}
	if configured, _ := credentials.Configured(ctx, "audit"); configured {
		t.Fatalf("a failed channel write left a credential registration")
	}

	// An empty credential revokes the registration: the read stops reporting it.
	retired, err := writers.WriteChannel(ctx, owner, notification.ChannelWrite{
		Name: "hooks",
		Spec: notification.ChannelSpec{Kind: notification.ChannelKindWebhook, Endpoint: channelAddress},
	})
	if err != nil {
		t.Fatalf("write the channel without a credential: %v", err)
	}
	if retired.CredentialSet {
		t.Fatalf("the channel read reports a credential after an empty value revoked it")
	}
	if configured, err := credentials.Configured(ctx, "hooks"); err != nil || configured {
		t.Fatalf("the credential was not revoked: configured=%v err=%v", configured, err)
	}

	// The credential has no write entry of its own: every declared write
	// addresses a channel or subscription configuration (NOTIFY-C-5).
	entries := notification.WriteEntries()
	if len(entries) == 0 {
		t.Fatalf("the product declares no write entry")
	}
	for _, entry := range entries {
		path := strings.ToLower(entry.Path)
		if strings.Contains(path, "credential") || strings.Contains(path, "secret") {
			t.Errorf("write entry %q addresses a credential", entry.ID)
		}
		if !strings.Contains(entry.Path, notification.KindChannel) &&
			!strings.Contains(entry.Path, notification.KindSubscription) {
			t.Errorf("write entry %q does not address a configuration class: %s", entry.ID, entry.Path)
		}
		if entry.RequiredRole != string(notification.RoleOwner) {
			t.Errorf("write entry %q admits %q, want the Owner only", entry.ID, entry.RequiredRole)
		}
	}

	subscription, err := writers.WriteSubscription(ctx, owner, notification.SubscriptionWrite{
		Name: "apps",
		Spec: notification.SubscriptionSpec{
			Source:   notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"},
			Reasons:  []string{"Failed"},
			Channels: []string{"hooks"},
		},
	})
	if err != nil {
		t.Fatalf("write the subscription: %v", err)
	}
	record := notification.Record{
		Name: "record-1",
		Spec: notification.RecordSpec{
			Channel: "hooks", Subscription: "apps",
			Source:  notification.ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"},
			Attempt: 2, Result: notification.ResultFailed, Reason: "channel answered 500",
		},
	}
	reporting := notification.Reporting{
		Channels:      []notification.Channel{channel},
		Subscriptions: []notification.Subscription{subscription},
		Records:       []notification.Record{record},
	}
	projection, ok := reporting.Read(notification.ItemChannels)
	if !ok {
		t.Fatalf("the channel item has no projection")
	}

	// None of the declared surfaces carries the credential value.
	surfaces := map[string]string{
		"channel object":    encode(t, objects.written),
		"delivery record":   encode(t, record),
		"log":               logs.String(),
		"audit":             encode(t, audit.entries),
		"component status":  encode(t, notification.ComponentStatus([]notification.Channel{channel})),
		"page projection":   encode(t, projection),
		"channel read":      encode(t, channel),
		"subscription read": encode(t, subscription),
	}
	for name, surface := range surfaces {
		if strings.Contains(surface, channelSecret) {
			t.Errorf("%s carries the credential value", name)
		}
		if strings.Contains(surface, blockedSecret) {
			t.Errorf("%s carries the refused write's credential value", name)
		}
	}

	// The surfaces were actually written, so the absence above is not vacuous.
	if !strings.Contains(logs.String(), "objectName=hooks") || !strings.Contains(logs.String(), "result=succeeded") {
		t.Errorf("the write path logged no success line: %s", logs.String())
	}
	results := map[string]int{}
	for _, entry := range audit.entries {
		results[entry.Result]++
		if entry.Actor != "owner" && entry.Actor != "member" {
			t.Errorf("audit entry carries actor %q", entry.Actor)
		}
	}
	if results["succeeded"] < 2 {
		t.Errorf("the audit stream carries %d success entries, want the channel and subscription writes", results["succeeded"])
	}
	if results["rejected"] < 2 {
		t.Errorf("the audit stream carries %d rejection entries, want both refused writes", results["rejected"])
	}

	// The credential renders redacted in every form a surface could print it.
	credential := notification.NewCredential(channelSecret)
	for _, rendered := range []string{
		fmt.Sprint(credential),
		fmt.Sprintf("%+v", credential),
		fmt.Sprintf("%#v", credential),
	} {
		if rendered != notification.Redacted {
			t.Errorf("credential prints as %q, want %q", rendered, notification.Redacted)
		}
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("encode the credential: %v", err)
	}
	if string(encoded) != `"`+notification.Redacted+`"` {
		t.Errorf("credential encodes as %s", encoded)
	}
	var credentialLog bytes.Buffer
	slog.New(slog.NewTextHandler(&credentialLog, nil)).Info("delivery", "credential", credential)
	if strings.Contains(credentialLog.String(), channelSecret) {
		t.Errorf("a structured log carries the credential value")
	}
}

// assertReadSurface holds the read side to KOSD-C-14: the credential store
// reports whether a value is set and has no operation that returns it.
func assertReadSurface(t *testing.T) {
	t.Helper()
	store := reflect.TypeOf((*notification.CredentialStore)(nil)).Elem()
	if store.NumMethod() != 2 {
		t.Fatalf("the credential store exposes %d operations, want Put and Configured", store.NumMethod())
	}
	for index := range store.NumMethod() {
		method := store.Method(index)
		switch method.Name {
		case "Put":
		case "Configured":
			if method.Type.NumOut() != 2 || method.Type.Out(0).Kind() != reflect.Bool {
				t.Fatalf("Configured returns %v, want a boolean and an error", method.Type)
			}
		default:
			t.Fatalf("the credential store exposes %q, which is not a write or a set-check", method.Name)
		}
	}
}

// encode renders one surface as JSON.
func encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode a surface: %v", err)
	}
	return string(encoded)
}
