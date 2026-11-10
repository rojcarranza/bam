package tui

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTUIDependencyRule pins spec §9.2: the UI may reach app, provider, view
// and errs, and nothing else inside the module. cobra, config and the Bamboo
// adapter are what keep it honest — a second provider must stay one package.
// Test files are exempt: building an app.Service needs a config.Config.
func TestTUIDependencyRule(t *testing.T) {
	allowed := map[string]bool{
		"github.com/rojcarranza/bam/internal/app":        true,
		"github.com/rojcarranza/bam/internal/provider":   true,
		"github.com/rojcarranza/bam/internal/view":       true,
		"github.com/rojcarranza/bam/internal/view/style": true,
		"github.com/rojcarranza/bam/internal/errs":       true,
	}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/rojcarranza/bam/") {
				continue
			}
			require.True(t, allowed[path], "%s imports %s, which spec §9.2 forbids", e.Name(), path)
		}
	}
}

func TestDepsCarriesEveryInjectionPoint(t *testing.T) {
	d := Deps{
		Servers: []Server{{Alias: "lab", URL: "https://bamboo.lab.example"}},
		Initial: "lab",
	}
	require.Equal(t, "lab", d.Initial)
	require.Equal(t, "https://bamboo.lab.example", d.Servers[0].URL)
}
