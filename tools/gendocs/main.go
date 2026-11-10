// Command gendocs writes the Markdown command reference to docs/cli.
package main

import (
	"log"
	"os"

	"github.com/rojcarranza/bam/internal/cli"
	"github.com/spf13/cobra/doc"
)

func main() {
	if err := os.MkdirAll("docs/cli", 0o755); err != nil {
		log.Fatal(err)
	}
	if err := doc.GenMarkdownTree(cli.DocsRoot(), "docs/cli"); err != nil {
		log.Fatal(err)
	}
}
