package notification

import (
	"slices"
	"sort"
	"testing"

	"github.com/tsic404/kos-sdk/extension"
	"github.com/tsic404/kos-sdk/manifest"
)

// TestManifestUsesTheFrameworkFieldSet checks the declaration's closed field
// set and every contribution the product claims (KOSD-C-18, SDK-C-3).
func TestManifestUsesTheFrameworkFieldSet(t *testing.T) {
	declared := sortedKeys(Manifest())
	expected := append([]string(nil), manifest.ExtensionManifestFields...)
	sort.Strings(expected)
	if !slices.Equal(declared, expected) {
		t.Fatalf("the declaration carries %v, the framework fixes %v", declared, expected)
	}

	checked, err := Validated()
	if err != nil {
		t.Fatalf("the declaration is invalid: %v", err)
	}
	if checked.Identifier != Identifier || checked.Version != Version {
		t.Fatalf("the declaration identifies %s@%s", checked.Identifier, checked.Version)
	}
	if checked.APIGroup != APIGroup || checked.FrameworkVersion != FrameworkVersion {
		t.Errorf("the declaration targets %q on framework %q", checked.APIGroup, checked.FrameworkVersion)
	}

	if len(checked.Contributions.ObjectClasses) != 3 {
		t.Fatalf("the declaration names %d object classes, want 3", len(checked.Contributions.ObjectClasses))
	}
	for _, class := range checked.Contributions.ObjectClasses {
		if class.Group != APIGroup || !slices.Equal(class.Versions, []string{ObjectVersion}) {
			t.Errorf("class %s is declared as %s/%s", class.Kind, class.Group, class.Versions)
		}
		if class.Namespaced || class.Resource == "" {
			t.Errorf("class %s is declared namespaced=%v with resource %q", class.Kind, class.Namespaced, class.Resource)
		}
		if slices.Contains(extension.CoreObjectKinds, class.Kind) {
			t.Errorf("class %s is a kernel kind", class.Kind)
		}
	}

	if len(checked.Contributions.Controllers) != 1 {
		t.Fatalf("the declaration names %d controllers, want 1", len(checked.Contributions.Controllers))
	}
	controller := checked.Contributions.Controllers[0]
	if controller.ID != ControllerDelivery {
		t.Errorf("the controller is named %q", controller.ID)
	}
	if !slices.Equal(controller.Manages, []extension.GroupKind{{Group: APIGroup, Kind: KindDeliveryRecord}}) {
		t.Errorf("the controller serves %+v", controller.Manages)
	}
	if !slices.Equal(controller.Scope.Groups, []string{APIGroup}) ||
		len(controller.Scope.Kinds) != 0 || len(controller.Scope.Namespaces) != 0 {
		t.Errorf("the controller scope is %+v, want the extension's own group alone", controller.Scope)
	}

	// The declared writers are per contribution: channels and subscriptions are
	// the Owner's, and the record class names no user writer (KOSD-C-59,
	// NOTIFY-C-1, NOTIFY-C-3).
	writers := map[string][]manifest.Role{}
	for _, permission := range checked.Permissions {
		if len(permission.ObjectWriters) > 0 {
			writers[permission.Contribution] = permission.ObjectWriters
		}
	}
	if !slices.Equal(writers[KindChannel], []manifest.Role{manifest.RoleOwner}) {
		t.Errorf("the channel writers are %v", writers[KindChannel])
	}
	if !slices.Equal(writers[KindSubscription], []manifest.Role{manifest.RoleOwner}) {
		t.Errorf("the subscription writers are %v", writers[KindSubscription])
	}
	if declared, named := writers[KindDeliveryRecord]; named {
		t.Errorf("the delivery record names the user writers %v; no user role may write it", declared)
	}

	// The controller reads the core event stream and the extension's own
	// classes; the core group is the empty string, never an invented name
	// (KOSD-C-64, NOTIFY-C-2).
	var delivery *manifest.ExtensionPermission
	for index := range checked.Permissions {
		if checked.Permissions[index].Contribution == ControllerDelivery {
			delivery = &checked.Permissions[index]
		}
	}
	if delivery == nil {
		t.Fatalf("the delivery controller declares no read scope")
	}
	if !slices.Equal(delivery.ReadScope.Groups, []string{APIGroup, ""}) {
		t.Errorf("the delivery read groups are %q, want the extension group and the core group", delivery.ReadScope.Groups)
	}
	for _, kind := range []extension.GroupKind{
		{Kind: CoreKindEvent},
		{Group: APIGroup, Kind: KindChannel},
		{Group: APIGroup, Kind: KindSubscription},
		{Group: APIGroup, Kind: KindDeliveryRecord},
	} {
		if !slices.Contains(delivery.ReadScope.Kinds, kind) {
			t.Errorf("the delivery controller does not declare reading %+v", kind)
		}
	}
	for _, kind := range delivery.ReadScope.Kinds {
		if kind.Kind == CoreKindEvent && kind.Group != "" {
			t.Errorf("the event read names the group %q; the core group is empty", kind.Group)
		}
	}

	if len(checked.Configuration.StatusWriters) != 0 {
		t.Errorf("the declaration lets the roles %v write extension object status", checked.Configuration.StatusWriters)
	}
	for _, condition := range []string{ConditionReconciled, ConditionDeliveryFailed} {
		if !slices.Contains(checked.Configuration.Conditions, condition) {
			t.Errorf("the declared conditions %v omit %s", checked.Configuration.Conditions, condition)
		}
	}

	// The credential is a write-only sensitive field of the channel structure,
	// so no form reads it back (KOSD-C-14).
	channels, ok := checked.Configuration.Schema["channels"].(map[string]any)
	if !ok {
		t.Fatalf("the configuration schema declares no channel structure")
	}
	properties, ok := channels["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the channel structure declares no properties")
	}
	credential, ok := properties["credential"].(map[string]any)
	if !ok {
		t.Fatalf("the channel structure declares no credential")
	}
	if credential["sensitive"] != true || credential["writeOnly"] != true {
		t.Errorf("the credential field is %v, want sensitive and write-only", credential)
	}

	if len(checked.Retention) != 3 {
		t.Fatalf("the declaration names %d retention entries, want one per class", len(checked.Retention))
	}
	for _, kind := range []string{KindChannel, KindSubscription, KindDeliveryRecord} {
		entry, found := retentionOf(checked.Retention, kind)
		if !found || !entry.Keep {
			t.Errorf("class %s is declared %+v on uninstall, want it kept", kind, entry)
		}
	}

	if checked.Contributions.Capabilities != nil ||
		len(checked.Contributions.Settings) != 0 ||
		len(checked.Contributions.Apps) != 0 {
		t.Errorf("the declaration contributes a class the product does not ship")
	}
	if len(checked.Contributions.Pages) != 1 {
		t.Errorf("the declaration names %d pages, want 1", len(checked.Contributions.Pages))
	}
}

// retentionOf reads the entry covering one class kind.
func retentionOf(declared []manifest.Retention, kind string) (manifest.Retention, bool) {
	for _, entry := range declared {
		if entry.Resource == kind {
			return entry, true
		}
	}
	return manifest.Retention{}, false
}

// sortedKeys returns a record's keys in order.
func sortedKeys(record map[string]any) []string {
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
