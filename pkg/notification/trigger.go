package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TriggerKind distinguishes what a delivery is about (NOTIFY-C-2).
type TriggerKind string

const (
	// TriggerKindEvent is a matched run-kernel event.
	TriggerKindEvent TriggerKind = "Event"
	// TriggerKindState is an observed state transition of the source object.
	TriggerKindState TriggerKind = "State"
)

// Trigger is one delivery the controller owes: what happened, the subscription
// that matched and the channel it targets. Its identity is stable when the same
// event or the same observed state is read again, so the records of one
// delivery form one set and a replay adds nothing (NOTIFY-C-2).
type Trigger struct {
	// Kind is the event or the state transition.
	Kind TriggerKind
	// Subscription is the matching subscription's name.
	Subscription string
	// Channel is the target channel's name.
	Channel string
	// Source is the object that matched.
	Source ObjectRef
	// Key identifies what happened: the event name, or the digest of the
	// observed conditions.
	Key string
	// Reason is the event reason the message carries; a state change has none.
	Reason string
	// At is the moment the trigger happened.
	At time.Time
}

// recordPrefix is the name prefix every attempt record of one delivery carries.
func (t Trigger) recordPrefix() string {
	key := strings.Join([]string{
		string(t.Kind), t.Subscription, t.Channel,
		t.Source.APIGroup, t.Source.Kind, t.Source.Namespace, t.Source.Name,
		t.Key,
	}, "/")
	sum := sha256.Sum256([]byte(key))
	return "delivery-" + hex.EncodeToString(sum[:8]) + "-"
}

// recordName is the name of one attempt's record; the attempt number is part
// of the name, so a replay of the same attempt collides instead of adding a
// second record (NOTIFY-C-3).
func (t Trigger) recordName(attempt int) string {
	return t.recordPrefix() + strconv.Itoa(attempt)
}

// conditionKey digests the observed conditions and reports the newest
// transition. The transition time is part of the digest, so a replayed state
// keeps its identity while a condition that appears, changes value or
// transitions back is a new trigger (NOTIFY-C-2).
func conditionKey(conditions []Condition) (key string, newest time.Time, ok bool) {
	ordered := append([]Condition(nil), conditions...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].Type != ordered[right].Type {
			return ordered[left].Type < ordered[right].Type
		}
		return ordered[left].Reason < ordered[right].Reason
	})
	digest := sha256.New()
	for _, condition := range ordered {
		line := condition.Type + "\x00" + condition.Status + "\x00" + condition.Reason + "\x00" +
			condition.LastTransitionTime.UTC().Format(time.RFC3339Nano) + "\x00"
		digest.Write([]byte(line))
		if condition.LastTransitionTime.After(newest) {
			newest = condition.LastTransitionTime
		}
	}
	if newest.IsZero() {
		// Without any transition time the state cannot be shown to have changed
		// after the subscription was established.
		return "", newest, false
	}
	return hex.EncodeToString(digest.Sum(nil)), newest, true
}
