package harness

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tsic404/kos-notification/pkg/notification"
)

// ControlPlane is a fixture of the control plane the delivery controller reads
// and writes: the channel and subscription objects, the credential store, the
// run kernel's recent event stream and the delivery records. It is a test
// double, not an installation channel and not the run kernel; the acceptance
// cases use it to observe what the controller reads and what it writes.
type ControlPlane struct {
	// Served reports whether one object class is served. A disabled or
	// uninstalled component's class is not served, so no new object of it is
	// written (KOSD-C-19, KOSD-C-28); nil serves every class.
	Served func(kind string) bool

	mutex         sync.Mutex
	events        []notification.Event
	channels      map[string]notification.Channel
	secrets       map[string]string
	subscriptions map[string]notification.SubscriptionState
	states        map[notification.ObjectRef]notification.ObjectState
	sources       map[notification.ObjectRef]notification.Source
	records       []notification.Record
}

// NewControlPlane returns an empty control plane.
func NewControlPlane() *ControlPlane {
	return &ControlPlane{
		channels:      map[string]notification.Channel{},
		secrets:       map[string]string{},
		subscriptions: map[string]notification.SubscriptionState{},
		states:        map[notification.ObjectRef]notification.ObjectState{},
		sources:       map[notification.ObjectRef]notification.Source{},
	}
}

// Controller returns the delivery controller wired to this control plane, with
// the real webhook sender and the fixture clock.
func (c *ControlPlane) Controller(now time.Time) notification.Controller {
	return notification.Controller{
		Subscriptions: c,
		Channels:      c,
		Events:        c,
		States:        c,
		Secrets:       c,
		Records:       c,
		Sender:        notification.Webhook{},
		Now:           func() time.Time { return now },
	}
}

// AddEvent adds one event to the run kernel's recent event stream.
func (c *ControlPlane) AddEvent(event notification.Event) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.events = append(c.events, event)
}

// AddChannel configures one channel. An empty credential declares a channel
// without one, which is what a delivery records as a failure (NOTIFY-C-4).
func (c *ControlPlane) AddChannel(name string, spec notification.ChannelSpec, credential string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.channels[name] = notification.ProjectChannel(name, spec, credential != "")
	if credential == "" {
		delete(c.secrets, name)
		return
	}
	c.secrets[name] = credential
}

// AddSubscription establishes one subscription.
func (c *ControlPlane) AddSubscription(name string, spec notification.SubscriptionSpec, createdAt time.Time) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.subscriptions[name] = notification.SubscriptionState{Name: name, Spec: spec, CreatedAt: createdAt}
}

// RemoveSubscription deletes one subscription; a deleted subscription owes no
// further delivery (NOTIFY-C-2).
func (c *ControlPlane) RemoveSubscription(name string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.subscriptions, name)
}

// SubscriptionExists reports whether one subscription is still established
// (NOTIFY-C-6).
func (c *ControlPlane) SubscriptionExists(_ context.Context, name string) (bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	_, found := c.subscriptions[name]
	return found, nil
}

// AddSource stores one source object and the visibility its records are read
// under (KOSD-C-59, KOSD-C-64).
func (c *ControlPlane) AddSource(ref notification.ObjectRef, source notification.Source) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.sources[ref] = notification.Source{
		Members:     append([]string(nil), source.Members...),
		AllowGuests: source.AllowGuests,
		Owner:       source.Owner,
	}
}

// RemoveSource deletes one source object. Nothing the product stores holds it
// alive, so the delete succeeds while its delivery records remain
// (NOTIFY-C-6).
func (c *ControlPlane) RemoveSource(ref notification.ObjectRef) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if _, found := c.sources[ref]; !found {
		return fmt.Errorf("source %s is not stored", ref)
	}
	delete(c.sources, ref)
	return nil
}

// Source reads one source object and its visibility (KOSD-C-59, KOSD-C-64).
func (c *ControlPlane) Source(_ context.Context, ref notification.ObjectRef) (notification.Source, bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	source, found := c.sources[ref]
	return source, found, nil
}

// SetObjectState records the observed state of one source object.
func (c *ControlPlane) SetObjectState(ref notification.ObjectRef, conditions []notification.Condition) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.states[ref] = notification.ObjectState{Ref: ref, Conditions: append([]notification.Condition(nil), conditions...)}
}

// StoredRecords returns the delivery records the controller wrote, oldest
// first.
func (c *ControlPlane) StoredRecords() []notification.Record {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]notification.Record(nil), c.records...)
}

// Subscriptions lists the established subscriptions by name.
func (c *ControlPlane) Subscriptions(context.Context) ([]notification.SubscriptionState, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	subscriptions := make([]notification.SubscriptionState, 0, len(c.subscriptions))
	for _, subscription := range c.subscriptions {
		subscriptions = append(subscriptions, subscription)
	}
	sort.Slice(subscriptions, func(left, right int) bool { return subscriptions[left].Name < subscriptions[right].Name })
	return subscriptions, nil
}

// Channel reads one configured channel.
func (c *ControlPlane) Channel(_ context.Context, name string) (notification.Channel, bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	channel, found := c.channels[name]
	return channel, found, nil
}

// RecentEvents returns the run kernel's recent event stream.
func (c *ControlPlane) RecentEvents(context.Context) ([]notification.Event, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return append([]notification.Event(nil), c.events...), nil
}

// ObjectState reads one source object's observed state.
func (c *ControlPlane) ObjectState(_ context.Context, ref notification.ObjectRef) (notification.ObjectState, bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	state, found := c.states[ref]
	return state, found, nil
}

// Secret reads one channel's registered secret.
func (c *ControlPlane) Secret(_ context.Context, channel string) (string, bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	secret, found := c.secrets[channel]
	return secret, found, nil
}

// Records lists the delivery records of one target.
func (c *ControlPlane) Records(_ context.Context, query notification.RecordQuery) ([]notification.Record, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	records := []notification.Record{}
	for _, record := range c.records {
		if record.Spec.Channel != query.Channel ||
			record.Spec.Subscription != query.Subscription ||
			record.Spec.Source != query.Source {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

// RecordsOf lists every stored record of one source, oldest first
// (NOTIFY-C-6).
func (c *ControlPlane) RecordsOf(_ context.Context, source notification.ObjectRef) ([]notification.Record, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	records := []notification.Record{}
	for _, record := range c.records {
		if record.Spec.Source != source {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

// CreateRecord stores one attempt's record, refusing a name that is already
// stored (NOTIFY-C-2) and refusing any write to a class that is not served
// (KOSD-C-19).
func (c *ControlPlane) CreateRecord(_ context.Context, record notification.Record) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.Served != nil && !c.Served(notification.KindDeliveryRecord) {
		return fmt.Errorf("%s is not served: its component is disabled or uninstalled", notification.KindDeliveryRecord)
	}
	for _, stored := range c.records {
		if stored.Name == record.Name {
			return notification.ErrRecordExists
		}
	}
	c.records = append(c.records, record)
	return nil
}
