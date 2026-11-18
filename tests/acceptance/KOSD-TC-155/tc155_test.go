// Package kosdtc155 verifies the install and uninstall slice KOSD-AC-72 gives
// this product: the package is built by the one SDK path, installing it makes
// its declared contributions appear one-to-one, uninstalling withdraws them and
// keeps the data its declaration retains, the kernel surface does not change,
// the kernel's package path accepts the artifact, and a digest mismatch is
// refused (KOSD-AC-72, KOSD-TC-155).
package kosdtc155

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tsic404/kos-notification/pkg/notification"
	"github.com/tsic404/kos-notification/tests/acceptance/harness"

	sdk "github.com/tsic404/kos-sdk/extension"
	"github.com/tsic404/kos-sdk/manifest"
	kosd "github.com/tsic404/kosd/pkg/extension"
)

// reference is the package address the component names.
const reference = "oci://registry.kos.tsic.top/system/kos-notification:1.0.0"

// TestTC155InstallAndUninstall installs the package once and uninstalls it once.
func TestTC155InstallAndUninstall(t *testing.T) {
	artifact, archive, err := notification.Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}
	cluster := harness.New()
	before := cluster.Surface()

	if err := cluster.Install(notification.Identifier, reference, archive, artifact.Digest.String()); err != nil {
		t.Fatalf("install the package: %v", err)
	}
	installed := cluster.Surface()

	declaration, err := cluster.Manifest(notification.Identifier)
	if err != nil {
		t.Fatalf("read the installed declaration: %v", err)
	}

	// Uninstall keeps the delivery records: the record class is declared
	// retained and appears in the residual list (NOTIFY-C-6, KOSD-C-29).
	if !slices.ContainsFunc(declaration.Retention, func(entry manifest.Retention) bool {
		return entry.Resource == notification.KindDeliveryRecord && entry.Keep
	}) {
		t.Fatalf("the delivery record is not declared kept on uninstall: %+v", declaration.Retention)
	}

	// The visible contributions correspond one-to-one with the declaration.
	classes := cluster.ObjectClasses()
	if len(classes) != len(declaration.Contributions.ObjectClasses) || len(classes) != 3 {
		t.Fatalf("the component serves %d classes, the declaration names %d",
			len(classes), len(declaration.Contributions.ObjectClasses))
	}
	for index, class := range classes {
		declared := declaration.Contributions.ObjectClasses[index]
		if !reflect.DeepEqual(class, declared) {
			t.Errorf("served class %d is %+v, declared %+v", index, class, declared)
		}
		if class.Group != notification.APIGroup || !slices.Equal(class.Versions, []string{notification.ObjectVersion}) {
			t.Errorf("class %s is served from %s/%s", class.Kind, class.Group, class.Versions)
		}
	}
	served := map[string]bool{}
	for _, class := range classes {
		served[class.Kind] = true
	}
	for _, kind := range []string{notification.KindChannel, notification.KindSubscription, notification.KindDeliveryRecord} {
		if !served[kind] {
			t.Errorf("class %s is not served", kind)
		}
	}

	// The page appears with the declaration, and the page payload is what the
	// framework's page validator accepts.
	pages := cluster.Pages()
	if len(pages) != len(declaration.Contributions.Pages) || len(pages) != 1 {
		t.Fatalf("the component serves %d pages, the declaration names %d",
			len(pages), len(declaration.Contributions.Pages))
	}
	assertPageAcceptedByFramework(t, pages[0])

	// A record the delivery controller writes survives a disable and is
	// retained as a residual on uninstall (NOTIFY-C-3, NOTIFY-C-6,
	// KOSD-C-29).
	cluster.Seed(notification.KindDeliveryRecord, map[string]any{
		"kind": notification.KindDeliveryRecord, "name": "record-1",
		"spec": map[string]any{"channel": "hooks", "result": notification.ResultFailed},
	})
	if err := cluster.Disable(notification.Identifier); err != nil {
		t.Fatalf("disable the component: %v", err)
	}
	if pages := cluster.Pages(); len(pages) != 0 {
		t.Errorf("a disabled component still serves %d pages", len(pages))
	}
	if classes := cluster.ObjectClasses(); len(classes) != 0 {
		t.Errorf("a disabled component still serves %d classes", len(classes))
	}
	if retained := cluster.Objects(notification.KindDeliveryRecord); len(retained) != 1 {
		t.Errorf("a disabled component kept %d records, want 1", len(retained))
	}
	if err := cluster.Enable(notification.Identifier); err != nil {
		t.Fatalf("enable the component: %v", err)
	}

	residuals, err := cluster.Uninstall(notification.Identifier)
	if err != nil {
		t.Fatalf("uninstall the component: %v", err)
	}
	if pages := cluster.Pages(); len(pages) != 0 {
		t.Errorf("an uninstalled component still serves %d pages", len(pages))
	}
	if classes := cluster.ObjectClasses(); len(classes) != 0 {
		t.Errorf("an uninstalled component still serves %d classes", len(classes))
	}
	if len(residuals) != 1 || residuals[0].Kind != notification.KindDeliveryRecord {
		t.Fatalf("the residual list is %+v, want the retained delivery record", residuals)
	}
	if name, _ := residuals[0].Object["name"].(string); name != "record-1" {
		t.Errorf("the residual carries %q, want record-1", name)
	}

	// The kernel surface is unchanged: no kernel kind, no contributed
	// capability, and the one controller writes the extension's own class.
	if !reflect.DeepEqual(before.CoreKinds, installed.CoreKinds) {
		t.Errorf("the kernel kind set changed: %v -> %v", before.CoreKinds, installed.CoreKinds)
	}
	if len(installed.Capabilities) != 0 {
		t.Errorf("the package contributes capabilities: %v", installed.Capabilities)
	}
	for _, class := range classes {
		for _, core := range sdk.CoreObjectKinds {
			if class.Kind == core {
				t.Errorf("the package declares the kernel kind %s", core)
			}
		}
	}
	if !reflect.DeepEqual(installed.ControllerScope,
		[]string{notification.ControllerDelivery + ":" + notification.APIGroup + "/" + notification.KindDeliveryRecord}) {
		t.Errorf("the controller write scope is %v", installed.ControllerScope)
	}
	withdrawn := cluster.Surface()
	if len(withdrawn.ControllerScope) != 0 {
		t.Errorf("an uninstalled component keeps the controller scope %v", withdrawn.ControllerScope)
	}
}

// TestTC155DigestMismatchRefused installs a package whose digest does not match
// and checks that nothing appears (KOSD-C-17).
func TestTC155DigestMismatchRefused(t *testing.T) {
	_, archive, err := notification.Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}
	cluster := harness.New()
	wrong := "sha256:" + strings.Repeat("0", 64)
	err = cluster.Install(notification.Identifier, reference, archive, wrong)
	if err == nil {
		t.Fatalf("a package whose digest does not match was installed")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("the refusal does not name the digest: %v", err)
	}
	if pages := cluster.Pages(); len(pages) != 0 {
		t.Errorf("a refused install served %d pages", len(pages))
	}
	if classes := cluster.ObjectClasses(); len(classes) != 0 {
		t.Errorf("a refused install served %d classes", len(classes))
	}
}

// TestTC155KernelReadsThePackage checks the kernel's package path over this
// product's artifact: the digest the install request names covers the archive,
// the declaration parses, and every contribution loads without a class problem
// (KOSD-C-17, KOSD-AC-72).
func TestTC155KernelReadsThePackage(t *testing.T) {
	artifact, archive, err := notification.Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}

	installed := kosd.Package{Reference: reference, Digest: artifact.Digest.String(), Content: archive}
	read, err := installed.Verify()
	if err != nil {
		t.Fatalf("the kernel refused the package: %v", err)
	}
	if read.Digest != artifact.Digest.String() {
		t.Fatalf("the kernel reads the digest %q, the package carries %q", read.Digest, artifact.Digest)
	}

	loaded, err := read.Declaration.Load()
	if err != nil {
		t.Fatalf("the kernel refused the declaration: %v", err)
	}
	if len(loaded.Problems) != 0 {
		t.Fatalf("the kernel reports class problems: %+v", loaded.Problems)
	}
	if loaded.Identifier != notification.Identifier {
		t.Errorf("the kernel loaded the identifier %q", loaded.Identifier)
	}
	if len(loaded.Contributions.ObjectClasses) != 3 || len(loaded.Contributions.Controllers) != 1 {
		t.Errorf("the kernel loaded %d classes and %d controllers, want 3 and 1",
			len(loaded.Contributions.ObjectClasses), len(loaded.Contributions.Controllers))
	}
	if len(loaded.Contributions.Pages) != 1 {
		t.Errorf("the kernel loaded %d pages, want 1", len(loaded.Contributions.Pages))
	}
	if !slices.ContainsFunc(loaded.Retention, func(entry kosd.Retention) bool {
		return entry.Resource == notification.KindDeliveryRecord && entry.Keep
	}) {
		t.Errorf("the kernel carries the retention %+v", loaded.Retention)
	}
}

// TestTC155FrameworkRange checks the declared target range admits the framework
// version the kernel serves (KOSD-C-16).
func TestTC155FrameworkRange(t *testing.T) {
	constraint, err := kosd.ParseConstraint(notification.FrameworkVersion)
	if err != nil {
		t.Fatalf("the declared framework range does not parse: %v", err)
	}
	for _, version := range kosd.SupportedFrameworks() {
		if !constraint.Allows(version) {
			t.Errorf("the declared range %q does not admit the served framework %s",
				notification.FrameworkVersion, version)
		}
	}
}

// assertPageAcceptedByFramework decodes the declared page into the framework's
// page type and runs it through the kernel's page validator.
func assertPageAcceptedByFramework(t *testing.T, declared map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(declared)
	if err != nil {
		t.Fatalf("encode the declared page: %v", err)
	}
	var page kosd.Page
	if err := json.Unmarshal(encoded, &page); err != nil {
		t.Fatalf("the declared page is not the framework's page shape: %v", err)
	}
	if page.ID != notification.PageNotification {
		t.Fatalf("the page decodes with id %q", page.ID)
	}
	if len(page.Items) != 3 || len(page.Actions) != 6 {
		t.Fatalf("the page decodes with %d items and %d actions, want 3 and 6", len(page.Items), len(page.Actions))
	}
	declaration := kosd.Declaration{
		Identifier: notification.Identifier,
		Version:    notification.Version,
		Contributions: kosd.Contributions{
			Pages: []kosd.Page{page},
		},
	}
	loaded, err := declaration.Load()
	if err != nil {
		t.Fatalf("the kernel rejected the declaration: %v", err)
	}
	for _, problem := range loaded.Problems {
		if problem.Class == kosd.ClassPages {
			t.Fatalf("the kernel rejected the page: %v", problem.Items)
		}
	}
	if len(loaded.Contributions.Pages) != 1 {
		t.Fatalf("the kernel loaded %d pages, want 1", len(loaded.Contributions.Pages))
	}
}
