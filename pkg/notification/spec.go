package notification

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Rejection is a product refusal a caller can act on: a stable code and the
// reason the user reads (KOSD-C-12).
type Rejection struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// Error returns the user-facing reason.
func (r *Rejection) Error() string { return r.Reason }

// Stable rejection codes (KOSD-C-12).
const (
	CodeForbidden              = "Forbidden"
	CodeUndeclaredClass        = "UndeclaredClass"
	CodeUnsupportedChannelKind = "UnsupportedChannelKind"
	CodeInvalidChannel         = "InvalidChannel"
	CodeInvalidSubscription    = "InvalidSubscription"
)

// ChannelKindWebhook is the one channel type the product supports today
// (NOTIFY-C-1).
const ChannelKindWebhook = "webhook"

// SupportedChannelKinds is the type list the create entry shows and the write
// accepts; one list feeds both (NOTIFY-C-1).
func SupportedChannelKinds() []string { return []string{ChannelKindWebhook} }

// ChannelSpec is the non-sensitive part of a channel object's spec
// (NOTIFY-C-1). The credential is not part of it (NOTIFY-C-5).
type ChannelSpec struct {
	// Kind selects the sender, for example webhook.
	Kind string `json:"kind"`
	// Endpoint is the address the delivery posts to.
	Endpoint string `json:"endpoint"`
}

// Validate rejects a channel configuration the product does not accept, with
// the reason the user reads (NOTIFY-C-1).
func (s ChannelSpec) Validate() error {
	if !slices.Contains(SupportedChannelKinds(), s.Kind) {
		return &Rejection{Code: CodeUnsupportedChannelKind,
			Reason: fmt.Sprintf("不支持的渠道类型 %q；可用类型：%s", s.Kind, strings.Join(SupportedChannelKinds(), ", "))}
	}
	parsed, err := url.Parse(s.Endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return &Rejection{Code: CodeInvalidChannel,
			Reason: fmt.Sprintf("渠道 %q 的地址 %q 不是 http 或 https URL", s.Kind, s.Endpoint)}
	}
	return nil
}

// Channel is a channel configuration as a read returns it: the non-sensitive
// items and whether a credential is set, never its value (NOTIFY-C-5).
type Channel struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Endpoint      string `json:"endpoint"`
	CredentialSet bool   `json:"credentialSet"`
}

// ProjectChannel reads a channel configuration (NOTIFY-C-5).
func ProjectChannel(name string, spec ChannelSpec, credentialSet bool) Channel {
	return Channel{Name: name, Kind: spec.Kind, Endpoint: spec.Endpoint, CredentialSet: credentialSet}
}

// ObjectRef names one source object (NOTIFY-C-2).
type ObjectRef struct {
	APIGroup  string `json:"apiGroup,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// SubscriptionSpec is a subscription object's spec (NOTIFY-C-1): the match
// conditions and the target channel set.
type SubscriptionSpec struct {
	// Source selects the object whose events match.
	Source ObjectRef `json:"source"`
	// Reasons selects event reasons; an empty list admits every reason.
	Reasons []string `json:"reasons,omitempty"`
	// Channels lists the channel names one match is delivered to; at least one
	// is required, and one subscription may target several channels.
	Channels []string `json:"channels"`
}

// Validate rejects a subscription the product does not accept, with the reason
// the user reads (NOTIFY-C-1).
func (s SubscriptionSpec) Validate() error {
	if strings.TrimSpace(s.Source.Kind) == "" {
		return &Rejection{Code: CodeInvalidSubscription, Reason: "订阅没有指定来源对象的类型"}
	}
	if strings.TrimSpace(s.Source.Name) == "" {
		return &Rejection{Code: CodeInvalidSubscription, Reason: "订阅没有指定来源对象的名称"}
	}
	if len(s.Channels) == 0 {
		return &Rejection{Code: CodeInvalidSubscription, Reason: "订阅至少指向一个渠道"}
	}
	seen := make(map[string]bool, len(s.Channels))
	for _, channel := range s.Channels {
		if strings.TrimSpace(channel) == "" {
			return &Rejection{Code: CodeInvalidSubscription, Reason: "订阅的渠道集合里有空名称"}
		}
		if seen[channel] {
			return &Rejection{Code: CodeInvalidSubscription,
				Reason: fmt.Sprintf("订阅的渠道 %q 声明了两次", channel)}
		}
		seen[channel] = true
	}
	return nil
}

// Targets returns the target channel set of one subscription (NOTIFY-C-1).
func (s SubscriptionSpec) Targets() []string { return append([]string(nil), s.Channels...) }

// Subscription is a subscription as a read returns it.
type Subscription struct {
	Name     string    `json:"name"`
	Source   ObjectRef `json:"source"`
	Reasons  []string  `json:"reasons,omitempty"`
	Channels []string  `json:"channels"`
}

// ProjectSubscription reads a subscription (NOTIFY-C-1).
func ProjectSubscription(name string, spec SubscriptionSpec) Subscription {
	return Subscription{
		Name:     name,
		Source:   spec.Source,
		Reasons:  append([]string(nil), spec.Reasons...),
		Channels: spec.Targets(),
	}
}

// Delivery results of one attempt (NOTIFY-C-3).
const (
	ResultDelivered = "Delivered"
	ResultFailed    = "Failed"
)

// RecordSpec is the declared face of a delivery record (NOTIFY-C-3): the target
// channel, the source object, the attempt number, the result and the failure
// reason. Instances are written by the delivery controller; no user write
// exists.
type RecordSpec struct {
	// Channel is the target channel name.
	Channel string `json:"channel"`
	// Subscription is the rule that matched.
	Subscription string `json:"subscription"`
	// Source is the object the event was about.
	Source ObjectRef `json:"source"`
	// Attempt is the attempt number, starting at one.
	Attempt int `json:"attempt"`
	// Result is Delivered or Failed.
	Result string `json:"result"`
	// Reason explains a failed attempt.
	Reason string `json:"reason,omitempty"`
}

// Record is one delivery record as a read returns it.
type Record struct {
	Name string     `json:"name"`
	Spec RecordSpec `json:"spec"`
}
