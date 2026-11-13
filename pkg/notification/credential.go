package notification

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Redacted is what a credential renders as wherever a value is printed.
const Redacted = "[redacted]"

// Credential is a channel secret. It renders redacted in every form a value
// reaches a surface through: printing, JSON encoding and structured logging
// (NOTIFY-C-5). The only caller that reads the value is the control plane's own
// credential store, which persists it (KOSD-C-14).
type Credential struct {
	secret string
}

// NewCredential holds one channel secret.
func NewCredential(secret string) Credential { return Credential{secret: secret} }

// IsSet reports whether the credential carries a value.
func (c Credential) IsSet() bool { return c.secret != "" }

// Reveal returns the secret. It exists for the control plane's credential
// store alone; no read path and no declared surface calls it (KOSD-C-14).
func (c Credential) Reveal() string { return c.secret }

// String renders the credential redacted.
func (c Credential) String() string { return Redacted }

// GoString renders the credential redacted under %#v.
func (c Credential) GoString() string { return Redacted }

// MarshalJSON renders the credential redacted, so encoding a document that
// happens to carry it cannot publish the value.
func (c Credential) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// LogValue renders the credential redacted in a structured log record.
func (c Credential) LogValue() slog.Value { return slog.StringValue(Redacted) }

// CredentialStore is the control plane's own credential store (KOSD-C-14).
// Writing is the only operation; reading reports whether a credential is set
// and never returns the value, which is what the product's read surfaces use
// (NOTIFY-C-5).
type CredentialStore interface {
	// Put replaces the channel's registration with credential, or revokes it
	// when the credential is empty. On success it returns a non-nil Restore
	// that puts back the registration it replaced, so the channel object write
	// can fail without leaving a credential behind.
	Put(ctx context.Context, channel string, credential Credential) (Restore, error)
	// Configured reports whether a credential is registered for the channel.
	Configured(ctx context.Context, channel string) (bool, error)
}

// Restore puts back the credential registration that one Put replaced. It is a
// no-op when the channel had no registration.
type Restore func(ctx context.Context) error

// MemoryCredentials is an in-memory credential store for tests and for a
// control plane that holds its credentials in process (KOSD-C-14).
type MemoryCredentials struct {
	mutex  sync.Mutex
	byName map[string]Credential
}

// NewMemoryCredentials returns an empty credential store.
func NewMemoryCredentials() *MemoryCredentials {
	return &MemoryCredentials{byName: map[string]Credential{}}
}

// Put replaces one registration, or revokes it when the credential is empty.
func (m *MemoryCredentials) Put(_ context.Context, channel string, credential Credential) (Restore, error) {
	if channel == "" {
		return nil, fmt.Errorf("credential store: channel name is empty")
	}
	m.mutex.Lock()
	defer m.mutex.Unlock()
	previous, existed := m.byName[channel]
	if credential.IsSet() {
		m.byName[channel] = credential
	} else {
		delete(m.byName, channel)
	}
	return func(context.Context) error {
		m.mutex.Lock()
		defer m.mutex.Unlock()
		if existed {
			m.byName[channel] = previous
		} else {
			delete(m.byName, channel)
		}
		return nil
	}, nil
}

// Configured reports whether the channel has a credential.
func (m *MemoryCredentials) Configured(_ context.Context, channel string) (bool, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	_, ok := m.byName[channel]
	return ok, nil
}
