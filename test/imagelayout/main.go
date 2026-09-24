// Command imagelayout builds and verifies canonical container filesystem manifests.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "image-layout:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: imagelayout manifest|verify|expected arguments")
	}
	switch args[0] {
	case "manifest":
		if len(args) != 3 {
			return fmt.Errorf("usage: imagelayout manifest TAR OUTPUT")
		}
		return manifestTar(args[1], args[2])
	case "verify":
		if len(args) != 4 {
			return fmt.Errorf("usage: imagelayout verify BASE TARGET EXPECTED")
		}
		return verifyManifestFiles(args[1], args[2], args[3])
	case "expected":
		if len(args) != 4 {
			return fmt.Errorf("usage: imagelayout expected KIND REPO OUTPUT")
		}
		return writeExpectedManifest(args[1], args[2], args[3])
	default:
		return fmt.Errorf("unknown command")
	}
}
