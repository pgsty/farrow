// Command catalogexport writes the exact embedded Farrow image catalog for
// repository staging. Signing remains a separate catalogsign operation.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pgsty/farrow/internal/fsutil"
	"github.com/pgsty/farrow/internal/image"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: catalogexport <absolute-new-output-path>")
		os.Exit(2)
	}
	data, err := image.EmbeddedCatalogBytes()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	target := os.Args[1]
	if !filepath.IsAbs(target) {
		err = fmt.Errorf("output path must be absolute")
	} else if parent, parentErr := filepath.EvalSymlinks(filepath.Dir(target)); parentErr != nil {
		err = parentErr
	} else {
		err = fsutil.AtomicCreate(filepath.Join(parent, filepath.Base(target)), data, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
