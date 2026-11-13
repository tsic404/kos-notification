// Package notification is the kos-notification first-party extension: the
// channel, subscription and delivery-record classes, the page that shows them,
// the delivery controller, and the channel credential boundary. A credential is
// written with the channel configuration, is never read back, and never enters
// a declared surface (NOTIFY-C-1, NOTIFY-C-3, NOTIFY-C-5, NOTIFY-C-7;
// KOSD-C-14, KOSD-C-18).
package notification

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tsic404/kos-sdk/extension"
	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kos-sdk/validate"
)

// Package identity (KOSD-C-17).
const (
	// Identifier is the extension identity across versions.
	Identifier = "kos-notification"
	// Version is the package version this declaration belongs to.
	Version = "1.0.0"
	// APIGroup is the extension's own API group; no declared class uses the
	// kernel group (KOSD-C-19).
	APIGroup = "notification.kos.tsic.top"
	// ObjectVersion is the version the three classes are served in.
	ObjectVersion = "v1"
	// FrameworkVersion is the framework API version range the package targets.
	FrameworkVersion = ">=1.0 <2.0"
)

// Declared object classes (KOSD-C-19).
const (
	KindChannel        = "Channel"
	KindSubscription   = "Subscription"
	KindDeliveryRecord = "DeliveryRecord"

	ResourceChannel        = "channels"
	ResourceSubscription   = "subscriptions"
	ResourceDeliveryRecord = "deliveryrecords"
)

// ControllerDelivery is the controller contribution that writes the delivery
// records; the delivery unit implements it (KOSD-C-20, NOTIFY-C-3).
const ControllerDelivery = "delivery"

// CoreKindEvent is the core-group object the delivery controller reads: the run
// kernel's recent event stream (KOSD-C-64, NOTIFY-C-2).
const CoreKindEvent = "Event"

// Page and item identities of the notification page (KOSD-C-53, NOTIFY-C-7).
const (
	PageNotification         = "notification"
	ItemChannels             = "channels"
	ItemSubscriptions        = "subscriptions"
	ItemDeliveryRecords      = "delivery-records"
	ActionCreateChannel      = "create-channel"
	ActionEditChannel        = "edit-channel"
	ActionDeleteChannel      = "delete-channel"
	ActionCreateSubscription = "create-subscription"
	ActionEditSubscription   = "edit-subscription"
	ActionDeleteSubscription = "delete-subscription"
)

// Condition types the extension object classes carry (KOSD-C-07).
const (
	ConditionReconciled     = "Reconciled"
	ConditionDeliveryFailed = "DeliveryFailed"
)

// ReasonRecordsReadOnly is the reason the record item is read-only: the
// controller writes the records and no user may modify or delete them
// (NOTIFY-C-3, KOSD-C-53).
const ReasonRecordsReadOnly = "投递记录由投递控制器写入，用户不能修改或删除"

// Role is a KOS session role (KOSD-C-59).
type Role string

const (
	RoleOwner  Role = "Owner"
	RoleMember Role = "Member"
	RoleGuest  Role = "Guest"
)

// declaredObjectRoles is the single statement of the class write permissions
// (KOSD-C-18). The delivery record names no role: it is the controller's alone,
// so not even the Owner may write it through the control-plane API
// (NOTIFY-C-3).
var declaredObjectRoles = map[string][]Role{
	KindChannel:        {RoleOwner},
	KindSubscription:   {RoleOwner},
	KindDeliveryRecord: {},
}

// DeclaredObjectRoles returns the declared writer roles per class.
func DeclaredObjectRoles() map[string][]Role {
	roles := make(map[string][]Role, len(declaredObjectRoles))
	for kind, writers := range declaredObjectRoles {
		roles[kind] = append([]Role(nil), writers...)
	}
	return roles
}

// AllowWrite reports whether role may write the declared class. The product's
// write path applies this before it writes anything (KOSD-C-59).
func AllowWrite(kind string, role Role) error {
	writers, declared := declaredObjectRoles[kind]
	if !declared {
		return &Rejection{Code: CodeUndeclaredClass,
			Reason: fmt.Sprintf("%s 不是本扩展声明的对象类", kind)}
	}
	if slices.Contains(writers, role) {
		return nil
	}
	if len(writers) == 0 {
		return &Rejection{Code: CodeForbidden,
			Reason: fmt.Sprintf("%s 不授予任何用户角色写入 %s；该类只由投递控制器写入", Identifier, kind)}
	}
	names := make([]string, 0, len(writers))
	for _, writer := range writers {
		names = append(names, string(writer))
	}
	return &Rejection{Code: CodeForbidden,
		Reason: fmt.Sprintf("%s 不能写入 %s；声明允许的角色：%s", role, kind, strings.Join(names, ", "))}
}

// Manifest returns the extension declaration (KOSD-C-18). The field set is the
// framework's closed set: nothing is added and no missing item is defaulted
// (SDK-C-3).
func Manifest() map[string]any {
	return map[string]any{
		"identifier":       Identifier,
		"version":          Version,
		"frameworkVersion": FrameworkVersion,
		"apiGroup":         APIGroup,
		"contributions": map[string]any{
			"objectClasses": asRecords(objectClasses()),
			"controllers":   asRecords([]extension.ControllerDeclaration{controller()}),
			"pages":         asRecords(Pages()),
		},
		"dependencies":  map[string]any{},
		"permissions":   asRecords(permissions()),
		"configuration": asRecord(configuration()),
		"retention":     asRecords(retention()),
	}
}

// Validated validates the declaration and returns it as the framework reads it.
func Validated() (manifest.ExtensionManifest, error) {
	checked := manifest.ValidateExtensionManifest(Manifest())
	if !checked.OK() {
		return manifest.ExtensionManifest{}, &validate.InvalidError{
			What:   "extension declaration",
			Issues: checked.Issues,
		}
	}
	return *checked.Manifest, nil
}

// objectClasses declares the three classes in the extension's own group and
// version; none is namespaced (KOSD-C-19, NOTIFY-C-1).
func objectClasses() []extension.ObjectClassDeclaration {
	return []extension.ObjectClassDeclaration{
		{Group: APIGroup, Kind: KindChannel, Resource: ResourceChannel, Versions: []string{ObjectVersion}},
		{Group: APIGroup, Kind: KindSubscription, Resource: ResourceSubscription, Versions: []string{ObjectVersion}},
		{Group: APIGroup, Kind: KindDeliveryRecord, Resource: ResourceDeliveryRecord, Versions: []string{ObjectVersion}},
	}
}

// controller declares the delivery controller: it serves the delivery record
// class and writes only inside the extension's own group (KOSD-C-20).
func controller() extension.ControllerDeclaration {
	return extension.ControllerDeclaration{
		ID:      ControllerDelivery,
		Manages: []extension.GroupKind{{Group: APIGroup, Kind: KindDeliveryRecord}},
		Scope:   extension.ControllerScope{Groups: []string{APIGroup}},
	}
}

// permissions declares what the contributions read and which session roles may
// write the extension's objects (KOSD-C-18, KOSD-C-59). The writer vocabulary is
// session roles only, so the record class names none and the product keeps
// refusing every user role in its own write path (NOTIFY-C-3). The controller
// reads the core event group, named by the empty string (KOSD-C-64, NOTIFY-C-2).
func permissions() []manifest.ExtensionPermission {
	return []manifest.ExtensionPermission{
		{
			Contribution: ControllerDelivery,
			ReadScope: manifest.ReadScope{
				Groups: []string{APIGroup, ""},
				Kinds: []extension.GroupKind{
					{Group: APIGroup, Kind: KindChannel},
					{Group: APIGroup, Kind: KindSubscription},
					{Group: APIGroup, Kind: KindDeliveryRecord},
					{Kind: CoreKindEvent},
				},
			},
		},
		{Contribution: KindChannel, ObjectWriters: []manifest.Role{manifest.RoleOwner}},
		{Contribution: KindSubscription, ObjectWriters: []manifest.Role{manifest.RoleOwner}},
	}
}

// configuration is the channel and subscription structure the create and edit
// entries render, and the conditions the classes carry (KOSD-C-18). No session
// role writes extension object status; the controller does (KOSD-C-03).
func configuration() manifest.Configuration {
	return manifest.Configuration{
		Schema:     configSchema(),
		Conditions: []string{ConditionReconciled, ConditionDeliveryFailed},
	}
}

// retention keeps every class across an uninstall; the kernel matches an entry
// by the class kind and lists what it kept (KOSD-C-29, NOTIFY-C-6).
func retention() []manifest.Retention {
	return []manifest.Retention{
		{Resource: KindChannel, Keep: true},
		{Resource: KindSubscription, Keep: true},
		{Resource: KindDeliveryRecord, Keep: true},
	}
}

// configSchema is the configuration structure the create and edit entries
// render (KOSD-C-18): the channel type list the write accepts and the
// subscription shape. The credential carries the sensitive marker and is
// write-only, because no read returns it (KOSD-C-14).
func configSchema() map[string]any {
	return map[string]any{
		"channels": map[string]any{
			"type":     "object",
			"required": []any{"kind", "endpoint"},
			"properties": map[string]any{
				"kind":       map[string]any{"type": "string", "enum": stringsRecord(SupportedChannelKinds())},
				"endpoint":   map[string]any{"type": "string"},
				"credential": map[string]any{"type": "string", "sensitive": true, "writeOnly": true},
			},
		},
		"subscriptions": map[string]any{
			"type":     "object",
			"required": []any{"source", "channels"},
			"properties": map[string]any{
				"source": map[string]any{
					"type":     "object",
					"required": []any{"kind", "name"},
					"properties": map[string]any{
						"apiGroup":  map[string]any{"type": "string"},
						"kind":      map[string]any{"type": "string"},
						"namespace": map[string]any{"type": "string"},
						"name":      map[string]any{"type": "string"},
					},
				},
				"reasons":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"channels": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}},
			},
		},
	}
}

// asRecords converts a typed declaration to the JSON record the SDK validator
// reads. The field names come from the SDK types, so the declaration cannot
// drift from them.
func asRecords[T any](values []T) []any {
	records := make([]any, 0, len(values))
	for _, value := range values {
		records = append(records, asRecord(value))
	}
	return records
}

// asRecord converts one typed declaration to its JSON record.
func asRecord(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encode declaration: %v", err))
	}
	record := map[string]any{}
	if err := json.Unmarshal(encoded, &record); err != nil {
		panic(fmt.Sprintf("decode declaration: %v", err))
	}
	return record
}

// stringsRecord renders a string list as a JSON array.
func stringsRecord(values []string) []any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return items
}

// AuditEntry is one product write as the audit stream records it (KOSD-C-14):
// actor, object, verb, result and time. It carries no credential field.
type AuditEntry struct {
	Actor      string    `json:"actor"`
	ObjectKind string    `json:"objectKind"`
	ObjectName string    `json:"objectName"`
	Verb       string    `json:"verb"`
	Result     string    `json:"result"`
	At         time.Time `json:"at"`
}

// AuditWriter records product writes in the control plane's audit stream
// (KOSD-C-14).
type AuditWriter interface {
	Record(entry AuditEntry)
}
