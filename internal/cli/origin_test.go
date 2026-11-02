package cli

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestServerForOrigin(t *testing.T) {
	cfg := &config.Config{Machine: &config.MachineFile{Servers: map[string]config.Server{
		"zeta":  {URL: "https://bamboo.example.com/"},
		"alpha": {URL: "https://bamboo.example.com"},
		"lab":   {URL: "http://bamboo.lab.example:8085/bamboo"},
	}}}

	for range 20 { // map order varies run to run; the sorted first alias always wins
		s, ok := serverForOrigin(cfg, "https://bamboo.example.com")
		assert.True(t, ok)
		assert.Equal(t, "alpha", s.Alias)
	}

	s, ok := serverForOrigin(cfg, "http://bamboo.lab.example:8085")
	assert.True(t, ok)
	assert.Equal(t, "lab", s.Alias)

	_, ok = serverForOrigin(cfg, "https://other.example.com")
	assert.False(t, ok)
}
