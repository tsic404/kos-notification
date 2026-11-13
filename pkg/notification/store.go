package notification

import (
	"context"
	"errors"
	"time"
)

// ErrRecordExists reports a record name that is already stored: the same event
// or the same observed state was read again, so the delivery is not repeated
// (NOTIFY-C-2).
var ErrRecordExists = errors.New("delivery record already exists")

// SubscriptionState is one established subscription as the delivery controller
// reads it. A disabled or deleted subscription is not listed, so no new
// delivery starts for it (NOTIFY-C-2).
type SubscriptionState struct {
	// Name is the subscription object name.
	Name string
	// Spec is the match condition and target channel set.
	Spec SubscriptionSpec
	// CreatedAt is the moment the subscription was established. An event or a
	// state change older than it is not delivered, so nothing is backfilled
	// (NOTIFY-C-2). The zero time means the moment is unknown and no delivery
	// is made for this subscription.
	CreatedAt time.Time
}

// Event is one recent run-kernel event (KOSD-C-64).
type Event struct {
	// Name is the event's identity in the run kernel.
	Name string
	// Source is the object the event is about.
	Source ObjectRef
	// Reason is the event reason a subscription matches.
	Reason string
	// LastSeen is the moment the event last happened.
	LastSeen time.Time
}

// Condition is one status condition of a source object (NOTIFY-C-2).
type Condition struct {
	// Type is the condition type.
	Type string
	// Status is the value the source object reports for it.
	Status string
	// Reason explains the value.
	Reason string
	// LastTransitionTime is when the value last changed. A condition without it
	// cannot be dated against the subscription's start.
	LastTransitionTime time.Time
}

// ObjectState is one source object as the controller observes it (NOTIFY-C-2).
type ObjectState struct {
	// Ref is the object the state belongs to.
	Ref ObjectRef
	// Conditions are the conditions its status carries.
	Conditions []Condition
}

// RecordQuery selects the delivery records of one target: the channel, the
// subscription and the source object (NOTIFY-C-3).
type RecordQuery struct {
	Channel      string
	Subscription string
	Source       ObjectRef
}

// SubscriptionReader lists the subscriptions that are still established
// (NOTIFY-C-2).
type SubscriptionReader interface {
	Subscriptions(ctx context.Context) ([]SubscriptionState, error)
}

// ChannelReader reads one target channel (NOTIFY-C-1).
type ChannelReader interface {
	Channel(ctx context.Context, name string) (Channel, bool, error)
}

// EventSource reads the run kernel's events. The kernel's own read is windowed
// to the hour that ends at the read (KOSD-C-64); the controller applies the
// window again so an adapter that returns more cannot cause a backfill
// (NOTIFY-C-2).
type EventSource interface {
	RecentEvents(ctx context.Context) ([]Event, error)
}

// ObjectStateReader reads the observed state of one source object
// (NOTIFY-C-2).
type ObjectStateReader interface {
	ObjectState(ctx context.Context, ref ObjectRef) (ObjectState, bool, error)
}

// RecordStore reads the records of one target and stores the record of one
// attempt. The delivery records are the controller's only write (KOSD-C-20).
type RecordStore interface {
	// Records lists the records of one target; the controller groups them by
	// the delivery their name carries.
	Records(ctx context.Context, query RecordQuery) ([]Record, error)
	// CreateRecord stores one attempt's record. It returns ErrRecordExists when
	// a record of that name is already stored.
	CreateRecord(ctx context.Context, record Record) error
}

// SecretSource hands one channel's registered secret to the sender for one
// attempt. It is the delivery path's only read of a credential value and no
// declared surface calls it (NOTIFY-C-5, KOSD-C-14).
type SecretSource interface {
	// Secret returns the channel's secret; ok is false when no credential is
	// registered, which is a failed delivery rather than an unauthenticated one
	// (NOTIFY-C-4).
	Secret(ctx context.Context, channel string) (secret string, ok bool, err error)
}
