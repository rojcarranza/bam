package app

import (
	"testing"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestProjectKeysComesFromTheConfiguration(t *testing.T) {
	s := &Service{Cfg: config.Fixed(testConfig(t.TempDir())), Server: config.ResolvedServer{Alias: "work"}}
	assert.Equal(t, []string{"PROJ"}, s.ProjectKeys())

	assert.Nil(t, (&Service{}).ProjectKeys(), "no configuration means no scope")
}

func TestDefaultProjectComesFromTheConfiguration(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Project.DefaultProject = "proj"
	s := &Service{Cfg: config.Fixed(cfg), Server: config.ResolvedServer{Alias: "work"}}
	assert.Equal(t, "PROJ", s.DefaultProject())

	assert.Empty(t, (&Service{}).DefaultProject(), "no configuration means every project")
}
