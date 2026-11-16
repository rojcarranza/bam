package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Intent (BAM-42 P3): a long wait reads as days and hours from 48 hours.

func TestLongWaitsReadAsDaysFrom48Hours(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		days string
		ok   bool
	}{
		{47*time.Hour + 59*time.Minute, "", false},
		{48 * time.Hour, "2d 0h", true},
		{1372*time.Hour + 36*time.Minute, "57d 4h", true},
	} {
		got, ok := LongWait(c.d)
		assert.Equal(t, c.ok, ok, c.d)
		assert.Equal(t, c.days, got, c.d)
	}
	assert.Equal(t, "47h59m", shortDuration(47*time.Hour+59*time.Minute))
	assert.Equal(t, "57d 4h", shortDuration(1372*time.Hour+36*time.Minute))
}
