package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSourceNoticesAFileThatAppearsLater(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	presets := filepath.Join(root, "bam", "presets.yaml")
	src, err := NewSource(func() LoadOptions {
		return LoadOptions{WorkDir: root, Home: root, MachineFile: filepath.Join(root, "m.yaml"), PresetsFile: presets,
			Getenv: func(string) string { return "" }}
	})
	require.NoError(t, err)
	cfg, err := src.Current()
	require.NoError(t, err)
	again, _ := src.Current()
	require.Same(t, cfg, again, "nothing changed, so the same configuration comes back")

	project := filepath.Join(root, ".bam.yaml")
	require.NoError(t, os.WriteFile(project, []byte("version: 1\nservers: {work: {url: https://bamboo.example.com}}\ntargets:\n  build: {plan: PROJ-BUILD}\n"), 0o644))
	fresh, err := src.Current()
	require.NoError(t, err)
	_, ok, _ := fresh.Target("build")
	require.True(t, ok, "a project file created while open is read")

	require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(presets), "presets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(presets), "presets", "ops.yaml"),
		[]byte("version: 1\npresets:\n  deploy: {plan: OPS-DEPLOY}\n"), 0o644))
	at := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(filepath.Dir(presets), "presets", "ops.yaml"), at, at))
	fresh, err = src.Current()
	require.NoError(t, err)
	_, ok, _ = fresh.Target("deploy")
	require.True(t, ok, "a presets file added while open is read")
}
