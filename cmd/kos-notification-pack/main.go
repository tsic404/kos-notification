// Command kos-notification-pack writes the extension package through the SDK's
// one packing path (SDK-C-4) and prints the digest an install request names.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsic404/kos-notification/pkg/notification"
)

func main() {
	out := flag.String("out",
		filepath.Join("dist", notification.Identifier+"-"+notification.Version+".tar.gz"),
		"package artifact path")
	flag.Parse()

	pkg, archive, err := notification.Artifact()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pack %s: %v\n", notification.Identifier, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", filepath.Dir(*out), err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, archive, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("package: %s\n", *out)
	fmt.Printf("digest: %s\n", pkg.Digest)
}
