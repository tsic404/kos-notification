package notification

import (
	"github.com/tsic404/kos-sdk/packaging"
)

// Artifact builds the extension package through the SDK's one packing path
// (SDK-C-4). The product ships no UI asset: its pages are server-side data
// projections, so the package carries the declaration alone and the digest
// covers it.
func Artifact() (packaging.Package, []byte, error) {
	return packaging.Pack(Manifest(), nil)
}
