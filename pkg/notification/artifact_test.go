package notification

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/tsic404/kos-sdk/packaging"
)

// TestArtifactCarriesOnlyTheDeclaration checks the package the product ships:
// the SDK path verifies it, it carries no asset, and its entries correspond to
// the declaration (SDK-C-4).
func TestArtifactCarriesOnlyTheDeclaration(t *testing.T) {
	artifact, archive, err := Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}
	if len(artifact.Assets) != 0 {
		t.Fatalf("the package carries %d assets; the product ships none", len(artifact.Assets))
	}
	if !strings.HasPrefix(string(artifact.Digest), "sha256:") {
		t.Fatalf("the digest %q is not the wire form", artifact.Digest)
	}
	verified, err := packaging.Verify(archive, artifact.Digest)
	if err != nil {
		t.Fatalf("the SDK refuses its own package: %v", err)
	}
	if len(verified.Assets) != 0 {
		t.Errorf("the verified package carries %d assets", len(verified.Assets))
	}
	declaration, err := Validated()
	if err != nil {
		t.Fatalf("the declaration is invalid: %v", err)
	}
	packaged, err := json.Marshal(verified.Manifest)
	if err != nil {
		t.Fatalf("encode the packaged declaration: %v", err)
	}
	declared, err := json.Marshal(declaration)
	if err != nil {
		t.Fatalf("encode the declared declaration: %v", err)
	}
	if !bytes.Equal(packaged, declared) {
		t.Errorf("the packaged declaration differs from the declared one")
	}
	names := entryNames(t, archive)
	if !slices.Equal(names, []string{packaging.ManifestEntry, packaging.DigestEntry}) {
		t.Errorf("the package entries are %v, want the manifest and the digest", names)
	}
}

// TestDigestCoversTheDeclaration checks that changing the declaration changes
// the digest and that a package does not verify against another digest
// (SDK-C-4).
func TestDigestCoversTheDeclaration(t *testing.T) {
	first, firstArchive, err := Artifact()
	if err != nil {
		t.Fatalf("build the package: %v", err)
	}
	changed := Manifest()
	changed["version"] = "1.0.1"
	second, secondArchive, err := packaging.Pack(changed, nil)
	if err != nil {
		t.Fatalf("build the changed package: %v", err)
	}
	if first.Digest == second.Digest {
		t.Fatalf("a changed declaration kept the digest %s", first.Digest)
	}
	if _, err := packaging.Verify(secondArchive, first.Digest); err == nil {
		t.Fatalf("a package verified against another declaration's digest")
	}
	if _, err := packaging.Verify(firstArchive, first.Digest); err != nil {
		t.Fatalf("the unchanged package no longer verifies: %v", err)
	}
}

// TestDeclarationIsRefusedOutsideTheClosedSet checks that a declaration missing
// a required field or carrying an unknown one produces no installable artifact,
// and that no missing item is defaulted (SDK-C-3).
func TestDeclarationIsRefusedOutsideTheClosedSet(t *testing.T) {
	missing := Manifest()
	delete(missing, "id")
	if _, _, err := packaging.Pack(missing, nil); err == nil {
		t.Fatalf("a declaration without an identifier produced a package")
	}

	unknown := Manifest()
	unknown["settings"] = []any{}
	if _, _, err := packaging.Pack(unknown, nil); err == nil {
		t.Fatalf("a declaration with an unknown field produced a package")
	}

	noConfig := Manifest()
	delete(noConfig, "config")
	if _, _, err := packaging.Pack(noConfig, nil); err == nil {
		t.Fatalf("a declaration without a config block produced a package")
	}
}

// entryNames reads the archive's entry names in order.
func entryNames(t *testing.T, archive []byte) []string {
	t.Helper()
	decompressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("open the package archive: %v", err)
	}
	defer decompressed.Close()
	reader := tar.NewReader(decompressed)
	names := []string{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatalf("read the package archive: %v", err)
		}
		names = append(names, header.Name)
	}
}
