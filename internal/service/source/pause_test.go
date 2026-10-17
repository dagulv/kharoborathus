package source

import (
	"testing"
	"time"
)

func TestClipPauses(t *testing.T) {
	now := time.Now()
	week := now.Add(probeInterval)

	paused := map[string]time.Time{"revived": week, "dormant": week, "active": now.Add(5 * time.Minute)}

	clipPauses(paused, []Source{
		{ID: "revived", Rate: 15},
		{ID: "dormant", Rate: int(probeInterval / time.Minute)},
		{ID: "active", Rate: 15},
	}, now)

	if !paused["revived"].Equal(now.Add(15*time.Minute)) || !paused["dormant"].Equal(week) || !paused["active"].Equal(now.Add(5*time.Minute)) {
		t.Errorf("paused %v", paused)
	}
}
