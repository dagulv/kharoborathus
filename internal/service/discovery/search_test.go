package discovery

import (
	"testing"
	"time"
)

func TestNextPause(t *testing.T) {
	cases := []struct{ prev, want time.Duration }{
		{0, 30 * time.Minute},
		{30 * time.Minute, time.Hour},
		{16 * time.Hour, 24 * time.Hour},
		{24 * time.Hour, 24 * time.Hour},
	}

	for _, c := range cases {
		if got := nextPause(c.prev); got != c.want {
			t.Errorf("nextPause(%s) = %s, want %s", c.prev, got, c.want)
		}
	}
}
