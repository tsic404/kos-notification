package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// DefaultMaxAttempts is the default bound on one delivery: the first attempt
// and two retries. The bound is what makes a failed delivery terminal instead
// of endless (NOTIFY-C-4).
const DefaultMaxAttempts = 3

// EventWindow is the event read window KOSD-C-64 fixes: an event outside the
// hour that ends at the read is not delivered.
const EventWindow = time.Hour

// Controller delivers matched events and state changes to the subscribed
// channels and records every attempt. It owns no state beside the records it
// writes (NOTIFY-C-3): the record set is what a repeated round reads to decide
// whether a delivery is already done.
type Controller struct {
	// Subscriptions reads the established subscriptions.
	Subscriptions SubscriptionReader
	// Channels reads one target channel.
	Channels ChannelReader
	// Events reads the run kernel's recent events.
	Events EventSource
	// States reads the observed state of a source object.
	States ObjectStateReader
	// Secrets reads one channel's registered secret for an attempt.
	Secrets SecretSource
	// Records reads and writes the delivery records.
	Records RecordStore
	// Sender posts one message to one channel.
	Sender Sender
	// MaxAttempts bounds one delivery; values below one use
	// DefaultMaxAttempts.
	MaxAttempts int
	// Now supplies the round clock; defaults to time.Now.
	Now func() time.Time
	// Log records a failed attempt without the credential; nil discards it.
	Log *slog.Logger
}

// Report is what one delivery round produced. A round makes at most one attempt
// per delivery, so a retry is the next round's work (KOSD-C-06).
type Report struct {
	// Attempts counts the attempts this round made and recorded.
	Attempts int
	// Delivered counts the attempts a channel accepted.
	Delivered int
	// Failed counts the attempts a channel refused or could not receive.
	Failed int
	// Terminal counts the deliveries whose last attempt reached the bound.
	Terminal int
	// Settled counts the deliveries this round left alone: already delivered or
	// out of attempts.
	Settled int
	// Problems lists the deliveries this round could not make, such as a
	// subscription naming a channel that is not configured.
	Problems []string
}

// Sync runs one delivery round: it reads the established subscriptions, the
// window's events and the source objects' observed states, and advances every
// matched delivery by at most one attempt. A channel's failure stays that
// channel's record, so the other channels of the subscription and the other
// subscriptions continue (NOTIFY-C-4).
func (c *Controller) Sync(ctx context.Context) (Report, error) {
	report := Report{}
	if err := c.validate(); err != nil {
		return report, err
	}
	subscriptions, err := c.Subscriptions.Subscriptions(ctx)
	if err != nil {
		return report, fmt.Errorf("list the subscriptions: %w", err)
	}
	events, err := c.Events.RecentEvents(ctx)
	if err != nil {
		return report, fmt.Errorf("read the recent events: %w", err)
	}
	now := c.now()
	window := now.Add(-EventWindow)
	for _, subscription := range subscriptions {
		c.syncSubscription(ctx, subscription, events, window, now, &report)
	}
	return report, nil
}

// validate reports a controller that cannot run a round.
func (c *Controller) validate() error {
	switch {
	case c.Subscriptions == nil:
		return errors.New("notification: the delivery controller has no subscription reader")
	case c.Channels == nil:
		return errors.New("notification: the delivery controller has no channel reader")
	case c.Events == nil:
		return errors.New("notification: the delivery controller has no event reader")
	case c.States == nil:
		return errors.New("notification: the delivery controller has no object state reader")
	case c.Secrets == nil:
		return errors.New("notification: the delivery controller has no credential reader")
	case c.Records == nil:
		return errors.New("notification: the delivery controller has no record store")
	case c.Sender == nil:
		return errors.New("notification: the delivery controller has no sender")
	}
	return nil
}

// syncSubscription advances every delivery one subscription owes. Its channels
// are independent: a channel this round cannot read is reported and the others
// continue (NOTIFY-C-4).
func (c *Controller) syncSubscription(ctx context.Context, subscription SubscriptionState, events []Event, window, now time.Time, report *Report) {
	if subscription.CreatedAt.IsZero() {
		// Without the moment the subscription was established nothing can be
		// shown to have happened after it, so nothing is delivered rather than
		// backfilled (NOTIFY-C-2).
		report.Problems = append(report.Problems,
			fmt.Sprintf("订阅 %s 没有建立时间，事件与状态变化不投递", subscription.Name))
	}
	triggers := matchedEventTriggers(subscription, events, window, now)
	if trigger, ok := c.stateTrigger(ctx, subscription, report); ok {
		triggers = append(triggers, trigger)
	}
	for _, channelName := range subscription.Spec.Channels {
		channel, found, err := c.Channels.Channel(ctx, channelName)
		if err != nil {
			report.Problems = append(report.Problems,
				fmt.Sprintf("读取渠道 %s 失败：%v", channelName, err))
			continue
		}
		if !found {
			report.Problems = append(report.Problems,
				fmt.Sprintf("订阅 %s 引用的渠道 %s 不存在", subscription.Name, channelName))
			continue
		}
		for _, trigger := range triggers {
			trigger.Channel = channel.Name
			c.advance(ctx, channel, trigger, report)
		}
	}
}

// matchedEventTriggers returns the events of the window that the subscription
// matches. An event before the subscription was established is not delivered,
// so nothing is backfilled (NOTIFY-C-2); an event outside the window is not
// read at all (KOSD-C-64).
func matchedEventTriggers(subscription SubscriptionState, events []Event, window, now time.Time) []Trigger {
	triggers := []Trigger{}
	for _, event := range events {
		if event.Name == "" {
			continue
		}
		if event.LastSeen.Before(window) || event.LastSeen.After(now) {
			continue
		}
		if subscription.CreatedAt.IsZero() || event.LastSeen.Before(subscription.CreatedAt) {
			continue
		}
		if !matchesSource(subscription.Spec.Source, event.Source) {
			continue
		}
		if !matchesReason(subscription.Spec.Reasons, event.Reason) {
			continue
		}
		triggers = append(triggers, Trigger{
			Kind:         TriggerKindEvent,
			Subscription: subscription.Name,
			Source:       event.Source,
			Key:          event.Name,
			Reason:       event.Reason,
			At:           event.LastSeen,
		})
	}
	return triggers
}

// stateTrigger returns the delivery one observed state transition owes, if any.
// The state as a whole is the trigger: a condition that appears, changes value
// or transitions back produces a new digest, while the same observed state
// replayed produces the same one (NOTIFY-C-2).
func (c *Controller) stateTrigger(ctx context.Context, subscription SubscriptionState, report *Report) (Trigger, bool) {
	state, found, err := c.States.ObjectState(ctx, subscription.Spec.Source)
	if err != nil {
		report.Problems = append(report.Problems,
			fmt.Sprintf("读取订阅 %s 的来源对象状态失败：%v", subscription.Name, err))
		return Trigger{}, false
	}
	if !found || state.Ref != subscription.Spec.Source || len(state.Conditions) == 0 {
		return Trigger{}, false
	}
	key, newest, ok := conditionKey(state.Conditions)
	if !ok {
		return Trigger{}, false
	}
	if subscription.CreatedAt.IsZero() || newest.Before(subscription.CreatedAt) {
		return Trigger{}, false
	}
	return Trigger{
		Kind:         TriggerKindState,
		Subscription: subscription.Name,
		Source:       state.Ref,
		Key:          key,
		At:           newest,
	}, true
}

// matchesSource reports whether a subscription's source object is the object
// the trigger is about. Every part of the identity is compared, so a
// subscription names one source object (NOTIFY-C-2).
func matchesSource(wanted, got ObjectRef) bool {
	return wanted == got
}

// matchesReason applies the subscription's reason list; an empty list admits
// every reason (NOTIFY-C-2).
func matchesReason(wanted []string, reason string) bool {
	return len(wanted) == 0 || slices.Contains(wanted, reason)
}

// advance brings one delivery at most one attempt further. The records already
// stored decide the attempt number and the outcome, so the same trigger read
// again adds nothing: a delivered delivery is left alone, and one at the bound
// stays failed with its last reason (NOTIFY-C-3, NOTIFY-C-4).
func (c *Controller) advance(ctx context.Context, channel Channel, trigger Trigger, report *Report) {
	records, err := c.Records.Records(ctx, RecordQuery{
		Channel:      trigger.Channel,
		Subscription: trigger.Subscription,
		Source:       trigger.Source,
	})
	if err != nil {
		report.Problems = append(report.Problems,
			fmt.Sprintf("读取订阅 %s 在渠道 %s 的投递记录失败：%v", trigger.Subscription, trigger.Channel, err))
		return
	}
	attempt, delivered := deliveryProgress(trigger, records)
	if delivered || attempt >= c.maxAttempts() {
		report.Settled++
		return
	}
	result, reason := c.deliver(ctx, channel, trigger)
	record := Record{
		Name: trigger.recordName(attempt + 1),
		Spec: RecordSpec{
			Channel:      trigger.Channel,
			Subscription: trigger.Subscription,
			Source:       trigger.Source,
			Attempt:      attempt + 1,
			Result:       result,
			Reason:       reason,
		},
	}
	switch err := c.Records.CreateRecord(ctx, record); {
	case errors.Is(err, ErrRecordExists):
		// The attempt was recorded by another round; it is not repeated.
		report.Settled++
	case err != nil:
		report.Problems = append(report.Problems,
			fmt.Sprintf("写入投递记录 %s 失败：%v", record.Name, err))
	default:
		report.Attempts++
		if result == ResultDelivered {
			report.Delivered++
		} else {
			report.Failed++
		}
		if record.Spec.Attempt >= c.maxAttempts() {
			report.Terminal++
		}
	}
}

// deliveryProgress reads how far one delivery got from its records: the highest
// attempt number and whether a channel already accepted it (NOTIFY-C-3).
func deliveryProgress(trigger Trigger, records []Record) (attempt int, delivered bool) {
	prefix := trigger.recordPrefix()
	for _, record := range records {
		if !strings.HasPrefix(record.Name, prefix) {
			continue
		}
		if record.Spec.Attempt > attempt {
			attempt = record.Spec.Attempt
		}
		if record.Spec.Result == ResultDelivered {
			delivered = true
		}
	}
	return attempt, delivered
}

// deliver makes one attempt and reports its result and failure reason. A
// channel without a credential and an unreachable channel are recorded
// failures, not silent drops (NOTIFY-C-4); the reason never carries the
// credential (NOTIFY-C-5).
func (c *Controller) deliver(ctx context.Context, channel Channel, trigger Trigger) (result, reason string) {
	if channel.Kind != ChannelKindWebhook {
		return ResultFailed, fmt.Sprintf("渠道 %s 的类型 %s 没有发送实现", channel.Name, channel.Kind)
	}
	secret, isSet, err := c.Secrets.Secret(ctx, channel.Name)
	if err != nil {
		return ResultFailed, fmt.Sprintf("读取渠道 %s 的凭据失败：%v", channel.Name, err)
	}
	if !isSet {
		return ResultFailed, fmt.Sprintf("渠道 %s 未设置凭据", channel.Name)
	}
	message := Message{
		Subscription: trigger.Subscription,
		Channel:      trigger.Channel,
		Source:       trigger.Source,
		Kind:         trigger.Kind,
		Reason:       trigger.Reason,
		At:           trigger.At.UTC(),
	}
	if err := c.Sender.Send(ctx, channel, secret, message); err != nil {
		c.logger().Warn("notification delivery failed",
			"channel", channel.Name, "subscription", trigger.Subscription, "error", err)
		return ResultFailed, err.Error()
	}
	return ResultDelivered, ""
}

// maxAttempts reads the round's attempt bound.
func (c *Controller) maxAttempts() int {
	if c.MaxAttempts > 0 {
		return c.MaxAttempts
	}
	return DefaultMaxAttempts
}

// now reads the round clock.
func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// logger returns the controller log.
func (c *Controller) logger() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
}
