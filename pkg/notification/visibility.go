package notification

import (
	"context"
	"fmt"
	"slices"
)

// KindApplication is the kernel kind whose events carry the application's own
// visibility instead of an owner (KOSD-C-59).
const KindApplication = "Application"

// Source is one source object as the record read sees it: an Application's
// visibility list, or the Member that owns another object (KOSD-C-59,
// KOSD-C-64).
type Source struct {
	// Members are the local usernames spec.visibility.members names; an
	// unresolvable name is simply absent.
	Members []string
	// AllowGuests is spec.visibility.allowGuests.
	AllowGuests bool
	// Owner is the local username of the Member that owns a non-Application
	// object.
	Owner string
}

// IsApplication reports whether the reference names an Application, whose
// records follow its spec visibility rather than an ownership relation
// (KOSD-C-59).
func (ref ObjectRef) IsApplication() bool {
	return ref.APIGroup == "" && ref.Kind == KindApplication
}

// String renders the identity that reasons and logs name.
func (ref ObjectRef) String() string {
	name := ref.Name
	if ref.Namespace != "" {
		name = ref.Namespace + "/" + ref.Name
	}
	if ref.APIGroup == "" {
		return ref.Kind + " " + name
	}
	return ref.APIGroup + "/" + ref.Kind + " " + name
}

// SourceReader resolves one source object. found is false once the object is
// deleted; the read marks that rather than failing (NOTIFY-C-6).
type SourceReader interface {
	Source(ctx context.Context, ref ObjectRef) (source Source, found bool, err error)
}

// SubscriptionLookup reports whether the subscription a record names still
// exists; a record outlives it (NOTIFY-C-6).
type SubscriptionLookup interface {
	SubscriptionExists(ctx context.Context, name string) (bool, error)
}

// RecordLister lists the stored delivery records of one source (NOTIFY-C-6).
type RecordLister interface {
	RecordsOf(ctx context.Context, source ObjectRef) ([]Record, error)
}

// RecordView is one delivery record as a read returns it: the stored record and
// whether the source and the subscription it names still exist (NOTIFY-C-6).
type RecordView struct {
	Record
	// SourceExists is false once the source object is deleted; the record is
	// still returned and marked.
	SourceExists bool `json:"sourceExists"`
	// SubscriptionExists is false once the matching subscription is deleted.
	SubscriptionExists bool `json:"subscriptionExists"`
}

// RecordSet is one read of the records of one source (NOTIFY-C-6).
type RecordSet struct {
	// Source is the source whose records were read.
	Source ObjectRef `json:"source"`
	// SourceExists is false once the source object is deleted.
	SourceExists bool `json:"sourceExists"`
	// Records is every stored record of the source, oldest first.
	Records []RecordView `json:"records"`
}

// RecordReader reads the delivery records under the visibility of their source
// (NOTIFY-C-6): an Application's records follow that application's visibility,
// and another object's records are visible to the Owner and to the Member that
// owns it. A source the session cannot see refuses the read with its reason, so
// an invisible source never reads as an empty record set (KOSD-C-59,
// KOSD-C-64).
type RecordReader struct {
	// Records lists the stored records of one source.
	Records RecordLister
	// Sources resolves the source object and its visibility.
	Sources SourceReader
	// Subscriptions reports whether one subscription still exists.
	Subscriptions SubscriptionLookup
}

// Read returns the records of one source as session may see them. The set
// carries whether the source still exists, because a record outlives the object
// it names (NOTIFY-C-6).
func (r RecordReader) Read(ctx context.Context, session Session, source ObjectRef) (RecordSet, error) {
	if r.Records == nil || r.Sources == nil || r.Subscriptions == nil {
		return RecordSet{}, fmt.Errorf("notification: the record read path is not configured")
	}
	resolved, exists, err := r.Sources.Source(ctx, source)
	if err != nil {
		return RecordSet{}, fmt.Errorf("read the source object %s: %w", source, err)
	}
	if err := canReadRecords(session, source, resolved, exists); err != nil {
		return RecordSet{}, err
	}
	records, err := r.Records.RecordsOf(ctx, source)
	if err != nil {
		return RecordSet{}, fmt.Errorf("read the delivery records of %s: %w", source, err)
	}
	set := RecordSet{Source: source, SourceExists: exists, Records: make([]RecordView, 0, len(records))}
	for _, record := range records {
		live, err := r.Subscriptions.SubscriptionExists(ctx, record.Spec.Subscription)
		if err != nil {
			return RecordSet{}, fmt.Errorf("read the subscription %s: %w", record.Spec.Subscription, err)
		}
		set.Records = append(set.Records, RecordView{Record: record, SourceExists: exists, SubscriptionExists: live})
	}
	return set, nil
}

// canReadRecords judges one read against the visibility of its source. Every
// refusal names what the session is missing, so a caller never mistakes it for
// a source without records (NOTIFY-C-6, KOSD-C-64).
func canReadRecords(session Session, ref ObjectRef, source Source, exists bool) error {
	if !exists {
		// A deleted source has no visibility left to judge, and only the Owner's
		// read does not depend on it (KOSD-C-59, KOSD-C-64).
		if session.Role == RoleOwner {
			return nil
		}
		return refuseRecords(fmt.Sprintf("来源对象 %s 已不存在，只有 Owner 能读取它的投递记录", ref))
	}
	if ref.IsApplication() {
		switch session.Role {
		case RoleOwner:
			return nil
		case RoleMember:
			if session.Username != "" && slices.Contains(source.Members, session.Username) {
				return nil
			}
			return refuseRecords(fmt.Sprintf(
				"Member %s 不在来源应用 %s 的可见成员中，读不到它的投递记录", session.Username, ref))
		case RoleGuest:
			if source.AllowGuests {
				return nil
			}
			return refuseRecords(fmt.Sprintf(
				"来源应用 %s 不允许访客可见，Guest 读不到它的投递记录", ref))
		}
		return refuseUndeclaredRole(session, ref)
	}
	switch session.Role {
	case RoleOwner:
		return nil
	case RoleMember:
		if session.Username != "" && session.Username == source.Owner {
			return nil
		}
		return refuseRecords(fmt.Sprintf(
			"Member %s 不拥有来源对象 %s，读不到它的投递记录", session.Username, ref))
	case RoleGuest:
		return refuseRecords(fmt.Sprintf(
			"来源 %s 不是应用，Guest 读不到它的投递记录", ref))
	}
	return refuseUndeclaredRole(session, ref)
}

// refuseRecords reports a read the source's visibility forbids.
func refuseRecords(reason string) error {
	return &Rejection{Code: CodeRecordNotVisible, Reason: reason}
}

// refuseUndeclaredRole reports a session whose role the KOS role model does not
// name; it reads nothing (KOSD-C-59).
func refuseUndeclaredRole(session Session, ref ObjectRef) error {
	return refuseRecords(fmt.Sprintf("角色 %q 不在 KOS 角色模型中，读不到 %s 的投递记录", session.Role, ref))
}
