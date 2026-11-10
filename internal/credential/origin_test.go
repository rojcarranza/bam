package credential

import (
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOriginNormalises(t *testing.T) {
	cases := map[string]string{
		"HTTPS://Bamboo.Example.com:443/bamboo/": "https://bamboo.example.com",
		"http://bamboo.lab.example:8085":         "http://bamboo.lab.example:8085",
		"http://bamboo.lab.example:80":           "http://bamboo.lab.example",
		" https://bamboo.example.com ":           "https://bamboo.example.com",
	}
	for in, want := range cases {
		got, err := Origin(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
}

func TestOriginRejectsBadURLs(t *testing.T) {
	for _, in := range []string{"bamboo.example.com", "ftp://bamboo.example.com", "https://", ""} {
		_, err := Origin(in)
		require.Error(t, err, in)
		assert.Equal(t, errs.KindConfig, errs.KindOf(err), in)
	}
}
