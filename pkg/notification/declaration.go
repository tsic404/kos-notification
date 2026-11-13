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

// StatusWriterController is the writer of an extension object's status when a
// controller of this package writes it (KOSD-C-18).
const StatusWriterController = "controller"

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
		"id":               Identifier,
		"version":          Version,
		"frameworkVersion": FrameworkVersion,
		"contributions": map[string]any{
			"objectClasses": asRecords(objectClasses()),
			"controllers":   asRecords([]extension.ControllerDeclaration{controller()}),
			"pages":         asRecords(Pages()),
		},
		"dependencies": map[string]any{
			"framework": FrameworkVersion,
		},
		"permissions": map[string]any{
			"write": asRecords([]manifest.WriteScope{controllerWriteScope()}),
			// The controller reads the run kernel's core event stream, which the
			// SDK's read scope cannot name: its apiGroup is required and the core
			// group is the empty string. No placeholder group is invented; the
			// gap is reported as a cross-repository interface misalignment.
			"objectRoles": objectRolesRecord(),
		},
		"config": map[string]any{"schema": configSchema()},
		"status": map[string]any{
			"component": map[string]any{
				"conditions": []any{ConditionReconciled},
			},
			"objectClasses": map[string]any{
				KindDeliveryRecord: map[string]any{
					"writer":     StatusWriterController,
					"conditions": []any{ConditionReconciled, ConditionDeliveryFailed},
				},
			},
		},
		// The contribution identity the declaration exposes is the class kind,
		// so the retention block is keyed by it (KOSD-C-29). Records survive the
		// uninstall and appear in the residual list (NOTIFY-C-6).
		"uninstallRetention": map[string]any{
			"contributions": map[string]any{
				KindChannel:        "retain",
				KindSubscription:   "retain",
				KindDeliveryRecord: "retain",
			},
		},
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

// objectClasses declares the three classes (KOSD-C-19). Their fields are the
// SDK's object class declaration; the product adds none.
func objectClasses() []extension.ObjectClassDeclaration {
	return []extension.ObjectClassDeclaration{
		{APIGroup: APIGroup, Version: ObjectVersion, Kind: KindChannel, Scope: extension.ScopeCluster},
		{APIGroup: APIGroup, Version: ObjectVersion, Kind: KindSubscription, Scope: extension.ScopeCluster},
		{APIGroup: APIGroup, Version: ObjectVersion, Kind: KindDeliveryRecord, Scope: extension.ScopeCluster},
	}
}

// controller declares the delivery controller and the class it writes
// (KOSD-C-20).
func controller() extension.ControllerDeclaration {
	return extension.ControllerDeclaration{
		Name: ControllerDelivery,
		WriteScopes: []extension.ControllerWriteScope{{
			APIGroup: APIGroup,
			Kind:     KindDeliveryRecord,
			Verbs:    writeVerbs(),
		}},
	}
}

// controllerWriteScope is the same grant in the permission block (KOSD-C-18).
func controllerWriteScope() manifest.WriteScope {
	return manifest.WriteScope{
		APIGroup:  APIGroup,
		Resources: []string{ResourceDeliveryRecord},
		Verbs:     writeVerbs(),
	}
}

// writeVerbs is the verb set the controller holds on its class (KOSD-C-20).
func writeVerbs() []string {
	return []string{"get", "list", "watch", "create", "update", "patch", "delete"}
}

// objectRolesRecord renders the declared writer roles (KOSD-C-18).
func objectRolesRecord() map[string]any {
	roles := make(map[string]any, len(declaredObjectRoles))
	for kind, writers := range declaredObjectRoles {
		names := make([]any, 0, len(writers))
		for _, writer := range writers {
			names = append(names, string(writer))
		}
		roles[kind] = names
	}
	return roles
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
