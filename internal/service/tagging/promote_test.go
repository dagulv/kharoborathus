package tagging

import "testing"

func TestConsistent(t *testing.T) {
	cases := []struct {
		matched, total int
		want           bool
	}{
		{2, 3, true},
		{3, 3, true},
		{1, 3, false},
		{0, 0, false},
	}

	for _, c := range cases {
		if got := consistent(c.matched, c.total); got != c.want {
			t.Errorf("consistent(%d, %d) = %v", c.matched, c.total, got)
		}
	}
}

func TestGateFor(t *testing.T) {
	cases := []struct {
		hits, total int
		want        bool
	}{
		{3, 100, true},
		{30, 100, false},
		{20, 100, false},
		{1, 10, false},
		{0, 0, false},
	}

	for _, c := range cases {
		if got := gateFor(c.hits, c.total); got != c.want {
			t.Errorf("gateFor(%d, %d) = %v", c.hits, c.total, got)
		}
	}
}

func TestMatchRate(t *testing.T) {
	if r := matchRate(5, 20); r != 0.25 {
		t.Errorf("rate %v", r)
	}

	if r := matchRate(0, 0); r != 0 {
		t.Errorf("empty rate %v", r)
	}
}
