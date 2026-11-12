package notification

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Session is the caller of a product write (KOSD-C-59).
type Session struct {
	// Username is the local account the control plane authenticated.
	Username string
	// Role is the session role the control plane evaluated.
	Role Role
}

// Object is one extension object the control plane stores.
type Object struct {
	Kind string
	Name string
	Spec any
}

// ObjectWriter writes extension objects through the control-plane API
// (KOSD-C-12). The product never writes a native resource directly.
type ObjectWriter interface {
	Put(ctx context.Context, object Object) error
}

// Writers is the product's write path for its configuration classes. The
// channel path is the only entry that accepts a credential, and it accepts it
// as part of the channel configuration (NOTIFY-C-5).
type Writers struct {
	// Objects writes the declared extension objects.
	Objects ObjectWriter
	// Credentials is the control plane's credential store (KOSD-C-14).
	Credentials CredentialStore
	// Audit records the write in the control plane's audit stream; nil skips it.
	Audit AuditWriter
	// Log records the write without the credential; nil discards it.
	Log *slog.Logger
	// Now supplies the audit clock; defaults to time.Now.
	Now func() time.Time
}

// ChannelWrite is one channel configuration write. The credential travels with
// it and with nothing else (NOTIFY-C-5); an empty value declares a channel
// without authentication and revokes a previously registered secret.
type ChannelWrite struct {
	// Name is the channel object name.
	Name string
	// Spec is the non-sensitive configuration.
	Spec ChannelSpec
	// Credential is the secret to register for this channel; it is write-only.
	Credential string
}

// SubscriptionWrite is one subscription configuration write (NOTIFY-C-1).
type SubscriptionWrite struct {
	// Name is the subscription object name.
	Name string
	// Spec is the match condition and target channel set.
	Spec SubscriptionSpec
}

// WriteChannel writes one channel configuration. The credential is applied
// before the configuration is written and restored when that write fails, so a
// channel never points at an unregistered secret and a failed write leaves no
// registration behind (NOTIFY-C-5, KOSD-C-12).
func (w Writers) WriteChannel(ctx context.Context, session Session, write ChannelWrite) (Channel, error) {
	if err := AllowWrite(KindChannel, session.Role); err != nil {
		w.finish(session, KindChannel, write.Name, "rejected")
		return Channel{}, err
	}
	if strings.TrimSpace(write.Name) == "" {
		w.finish(session, KindChannel, write.Name, "rejected")
		return Channel{}, &Rejection{Code: CodeInvalidChannel, Reason: "渠道配置没有指定名称"}
	}
	if err := write.Spec.Validate(); err != nil {
		w.finish(session, KindChannel, write.Name, "rejected")
		return Channel{}, err
	}
	if w.Objects == nil {
		w.finish(session, KindChannel, write.Name, "failed")
		return Channel{}, fmt.Errorf("notification: the object writer is not configured")
	}
	restore := Restore(func(context.Context) error { return nil })
	if w.Credentials != nil {
		applied, err := w.Credentials.Put(ctx, write.Name, NewCredential(write.Credential))
		if err != nil {
			w.finish(session, KindChannel, write.Name, "failed")
			return Channel{}, fmt.Errorf("register the credential of channel %s: %w", write.Name, err)
		}
		restore = applied
	} else if write.Credential != "" {
		w.finish(session, KindChannel, write.Name, "failed")
		return Channel{}, fmt.Errorf("notification: a credential was supplied but no credential store is configured")
	}
	configured, err := w.configured(ctx, write.Name)
	if err != nil {
		w.finish(session, KindChannel, write.Name, "failed")
		return Channel{}, restoreRegistration(ctx, restore, err)
	}
	if err := w.Objects.Put(ctx, Object{Kind: KindChannel, Name: write.Name, Spec: write.Spec}); err != nil {
		w.finish(session, KindChannel, write.Name, "failed")
		return Channel{}, restoreRegistration(ctx, restore, err)
	}
	w.finish(session, KindChannel, write.Name, "succeeded")
	return ProjectChannel(write.Name, write.Spec, configured), nil
}

// restoreRegistration undoes the credential write a failed channel write had
// already applied. A restore failure stays on the error because it leaves a
// side effect the caller has to know about (KOSD-C-12).
func restoreRegistration(ctx context.Context, restore Restore, cause error) error {
	if restore == nil {
		return cause
	}
	if err := restore(ctx); err != nil {
		return fmt.Errorf("%w; the credential registration was not restored: %v", cause, err)
	}
	return cause
}

// WriteSubscription writes one subscription configuration.
func (w Writers) WriteSubscription(ctx context.Context, session Session, write SubscriptionWrite) (Subscription, error) {
	if err := AllowWrite(KindSubscription, session.Role); err != nil {
		w.finish(session, KindSubscription, write.Name, "rejected")
		return Subscription{}, err
	}
	if strings.TrimSpace(write.Name) == "" {
		w.finish(session, KindSubscription, write.Name, "rejected")
		return Subscription{}, &Rejection{Code: CodeInvalidSubscription, Reason: "订阅没有指定名称"}
	}
	if err := write.Spec.Validate(); err != nil {
		w.finish(session, KindSubscription, write.Name, "rejected")
		return Subscription{}, err
	}
	if w.Objects == nil {
		w.finish(session, KindSubscription, write.Name, "failed")
		return Subscription{}, fmt.Errorf("notification: the object writer is not configured")
	}
	if err := w.Objects.Put(ctx, Object{Kind: KindSubscription, Name: write.Name, Spec: write.Spec}); err != nil {
		w.finish(session, KindSubscription, write.Name, "failed")
		return Subscription{}, err
	}
	w.finish(session, KindSubscription, write.Name, "succeeded")
	return ProjectSubscription(write.Name, write.Spec), nil
}

// configured reports whether the channel has a credential set.
func (w Writers) configured(ctx context.Context, channel string) (bool, error) {
	if w.Credentials == nil {
		return false, nil
	}
	configured, err := w.Credentials.Configured(ctx, channel)
	if err != nil {
		return false, fmt.Errorf("read whether channel %s has a credential: %w", channel, err)
	}
	return configured, nil
}

// finish records one write attempt: the audit entry carries the actor, object,
// verb, result and time; the log line carries no credential (KOSD-C-14).
func (w Writers) finish(session Session, kind, name, result string) {
	at := time.Now()
	if w.Now != nil {
		at = w.Now()
	}
	if w.Audit != nil {
		w.Audit.Record(AuditEntry{
			Actor:      session.Username,
			ObjectKind: kind,
			ObjectName: name,
			Verb:       "write",
			Result:     result,
			At:         at.UTC(),
		})
	}
	if w.Log != nil {
		w.Log.Info("notification configuration write",
			"actor", session.Username, "objectKind", kind, "objectName", name, "result", result)
	}
}

// WriteEntry is one write the product offers a page (KOSD-C-22).
type WriteEntry struct {
	ID           string `json:"id"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	RequiredRole string `json:"requiredRole,omitempty"`
}

// WriteEntries returns every write entry the product declares. The entries come
// from the page declaration, so the page and the write surface cannot diverge;
// no entry addresses the delivery records, which no user writes (NOTIFY-C-3).
func WriteEntries() []WriteEntry {
	entries := []WriteEntry{}
	for _, page := range Pages() {
		for _, action := range page.Actions {
			entries = append(entries, WriteEntry{
				ID:           action.ID,
				Method:       action.Method,
				Path:         action.Path,
				RequiredRole: action.RequiredRole,
			})
		}
	}
	return entries
}

// ComponentStatus is the component status interface (KOSD-C-18): the component
// conditions and one non-sensitive row per channel. Diagnostics and the
// component page read it; it carries no credential value (NOTIFY-C-5).
func ComponentStatus(channels []Channel) map[string]any {
	rows := make([]any, 0, len(channels))
	for _, channel := range channels {
		rows = append(rows, map[string]any{
			"name":          channel.Name,
			"kind":          channel.Kind,
			"endpoint":      channel.Endpoint,
			"credentialSet": channel.CredentialSet,
		})
	}
	return map[string]any{
		"conditions": []any{map[string]any{"type": ConditionReconciled, "status": "True"}},
		"channels":   rows,
	}
}
