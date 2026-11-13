package notification

import (
	"errors"
	"slices"
	"testing"
)

// TestOnlyListedChannelKindsAreAccepted checks the type list the create entry
// shows is the type list the write accepts, and that another type is refused
// with its reason (NOTIFY-C-1).
func TestOnlyListedChannelKindsAreAccepted(t *testing.T) {
	const endpoint = "https://hooks.example.test/kos"
	for _, kind := range SupportedChannelKinds() {
		if err := (ChannelSpec{Kind: kind, Endpoint: endpoint}).Validate(); err != nil {
			t.Errorf("the listed type %q is refused: %v", kind, err)
		}
	}
	for _, kind := range []string{"", "email", "slack"} {
		err := (ChannelSpec{Kind: kind, Endpoint: endpoint}).Validate()
		rejection := &Rejection{}
		if !errors.As(err, &rejection) || rejection.Code != CodeUnsupportedChannelKind {
			t.Errorf("the type %q was not refused as unsupported: %v", kind, err)
		}
		if rejection.Reason == "" {
			t.Errorf("the type %q was refused without a reason", kind)
		}
	}
}

// TestChannelEndpointIsJudged checks the address a channel names is a URL a
// sender can post to (NOTIFY-C-1).
func TestChannelEndpointIsJudged(t *testing.T) {
	for _, endpoint := range []string{"", "hooks.example.test/kos", "ftp://hooks.example.test/kos", "https://"} {
		err := (ChannelSpec{Kind: ChannelKindWebhook, Endpoint: endpoint}).Validate()
		rejection := &Rejection{}
		if !errors.As(err, &rejection) || rejection.Code != CodeInvalidChannel {
			t.Errorf("the endpoint %q was not refused: %v", endpoint, err)
		}
	}
}

// TestSubscriptionTargetsAreReadable checks the target channel set a
// subscription names (NOTIFY-C-1): several channels are kept, an empty set and
// a repeated channel are refused with their reasons.
func TestSubscriptionTargetsAreReadable(t *testing.T) {
	spec := SubscriptionSpec{
		Source:   ObjectRef{Kind: "Application", Namespace: "apps", Name: "shop"},
		Reasons:  []string{"Failed"},
		Channels: []string{"hooks", "audit"},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("a well-formed subscription was refused: %v", err)
	}
	if !slices.Equal(spec.Targets(), []string{"hooks", "audit"}) {
		t.Fatalf("the target channel set reads as %v", spec.Targets())
	}

	empty := spec
	empty.Channels = nil
	rejection := &Rejection{}
	if err := empty.Validate(); !errors.As(err, &rejection) || rejection.Code != CodeInvalidSubscription {
		t.Fatalf("a subscription without a channel was accepted: %v", err)
	}

	repeated := spec
	repeated.Channels = []string{"hooks", "hooks"}
	if err := repeated.Validate(); !errors.As(err, &rejection) || rejection.Code != CodeInvalidSubscription {
		t.Fatalf("a subscription naming a channel twice was accepted: %v", err)
	}

	noSource := spec
	noSource.Source = ObjectRef{}
	if err := noSource.Validate(); !errors.As(err, &rejection) || rejection.Code != CodeInvalidSubscription {
		t.Fatalf("a subscription without a source object was accepted: %v", err)
	}
}

// TestNoUserRoleWritesTheRecord checks the declared write permissions the
// product's write path applies (NOTIFY-C-3, KOSD-C-59).
func TestNoUserRoleWritesTheRecord(t *testing.T) {
	for _, role := range []Role{RoleOwner, RoleMember, RoleGuest} {
		err := AllowWrite(KindDeliveryRecord, role)
		rejection := &Rejection{}
		if !errors.As(err, &rejection) || rejection.Code != CodeForbidden {
			t.Errorf("%s could write a delivery record: %v", role, err)
		}
	}
	if err := AllowWrite(KindChannel, RoleOwner); err != nil {
		t.Errorf("the Owner cannot write a channel: %v", err)
	}
	if err := AllowWrite(KindChannel, RoleMember); err == nil {
		t.Errorf("a Member can write a channel")
	}
	if err := AllowWrite("Widget", RoleOwner); err == nil {
		t.Errorf("an undeclared class is writable")
	}
}
