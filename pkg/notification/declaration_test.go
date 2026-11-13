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
	if checked.ID != Identifier || checked.Version != Version {
		t.Fatalf("the declaration identifies %s@%s", checked.ID, checked.Version)
	}
	if checked.Dependencies.Framework != FrameworkVersion {
		t.Errorf("the framework dependency is %q, want %q", checked.Dependencies.Framework, FrameworkVersion)
	}

	if len(checked.Contributions.ObjectClasses) != 3 {
		t.Fatalf("the declaration names %d object classes, want 3", len(checked.Contributions.ObjectClasses))
	}
	for _, class := range checked.Contributions.ObjectClasses {
		if class.APIGroup != APIGroup || class.Version != ObjectVersion || class.Scope != extension.ScopeCluster {
			t.Errorf("class %s is declared as %s/%s %s", class.Kind, class.APIGroup, class.Version, class.Scope)
		}
		if slices.Contains(extension.CoreObjectKinds, class.Kind) {
			t.Errorf("class %s is a kernel kind", class.Kind)
		}
	}

	if len(checked.Contributions.Controllers) != 1 {
		t.Fatalf("the declaration names %d controllers, want 1", len(checked.Contributions.Controllers))
	}
	controller := checked.Contributions.Controllers[0]
	if controller.Name != ControllerDelivery {
		t.Errorf("the controller is named %q", controller.Name)
	}
	if len(controller.WriteScopes) != 1 ||
		controller.WriteScopes[0].APIGroup != APIGroup ||
		controller.WriteScopes[0].Kind != KindDeliveryRecord {
		t.Fatalf("the controller write scope is %+v", controller.WriteScopes)
	}
	if len(checked.Permissions.Write) != 1 ||
		checked.Permissions.Write[0].APIGroup != APIGroup ||
		!slices.Equal(checked.Permissions.Write[0].Resources, []string{ResourceDeliveryRecord}) ||
		!slices.Equal(checked.Permissions.Write[0].Verbs, controller.WriteScopes[0].Verbs) {
		t.Errorf("the permission write scope is %+v, the controller scope %+v",
			checked.Permissions.Write, controller.WriteScopes)
	}
	if len(checked.Permissions.Read) != 0 {
		t.Errorf("the declaration names read scopes %+v; the SDK cannot name the core event group", checked.Permissions.Read)
	}

	roles := checked.Permissions.ObjectRoles
	if !slices.Equal(roles[KindChannel], []manifest.Role{manifest.RoleOwner}) {
		t.Errorf("the channel writers are %v", roles[KindChannel])
	}
	if !slices.Equal(roles[KindSubscription], []manifest.Role{manifest.RoleOwner}) {
		t.Errorf("the subscription writers are %v", roles[KindSubscription])
	}
	if len(roles[KindDeliveryRecord]) != 0 {
		t.Errorf("the delivery record names the writers %v; no user role may write it", roles[KindDeliveryRecord])
	}

	status, ok := checked.Status.ObjectClasses[KindDeliveryRecord]
	if !ok {
		t.Fatalf("the delivery record declares no status interface")
	}
	if status.Writer != StatusWriterController ||
		!slices.Equal(status.Conditions, []string{ConditionReconciled, ConditionDeliveryFailed}) {
		t.Errorf("the delivery record status interface is %+v", status)
	}

	retention := checked.UninstallRetention.Contributions
	for _, kind := range []string{KindChannel, KindSubscription, KindDeliveryRecord} {
		if retention[kind] != manifest.Retain {
			t.Errorf("class %s is declared %q on uninstall, want %q", kind, retention[kind], manifest.Retain)
		}
	}

	if len(checked.Contributions.Capabilities) != 0 ||
		len(checked.Contributions.Settings) != 0 ||
		len(checked.Contributions.Apps) != 0 {
		t.Errorf("the declaration contributes a class the product does not ship")
	}
	if len(checked.Contributions.Pages) != 1 {
		t.Errorf("the declaration names %d pages, want 1", len(checked.Contributions.Pages))
	}

	// The credential is a write-only sensitive field of the channel structure,
	// so no form reads it back (KOSD-C-14).
	config, ok := Manifest()["config"].(map[string]any)
	if !ok {
		t.Fatalf("the declaration carries no config block")
	}
	schema, ok := config["schema"].(map[string]any)
	if !ok {
		t.Fatalf("the config block carries no schema")
	}
	channels, ok := schema["channels"].(map[string]any)
	if !ok {
		t.Fatalf("the schema declares no channel structure")
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
