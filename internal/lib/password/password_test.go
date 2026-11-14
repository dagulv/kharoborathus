package password

import (
	"regexp"
	"testing"
)

// Made by frontend/src/lib/server/password.ts for "correct horse".
const nodeHash = "9c780d1da3043c92cc80568436ccdd76:28c63a11510304627b450bec9da7b6a666b8781e05379214f9f7e3c926dd0e4e606c835e49055c5277311216d214d45af0c36b2228556563093821146d803d70"

func TestVerifyNodeHash(t *testing.T) {
	if ok, err := Verify("correct horse", nodeHash); !ok || err != nil {
		t.Errorf("right password: %v, %v", ok, err)
	}

	if ok, err := Verify("wrong", nodeHash); ok || err != nil {
		t.Errorf("wrong password: %v, %v", ok, err)
	}
}

func TestHashRoundTrip(t *testing.T) {
	h, err := Hash("pw")
	if err != nil {
		t.Fatal(err)
	}

	if !regexp.MustCompile(`^[0-9a-f]{32}:[0-9a-f]{128}$`).MatchString(h) {
		t.Errorf("format: %s", h)
	}

	if ok, err := Verify("pw", h); !ok || err != nil {
		t.Errorf("right password: %v, %v", ok, err)
	}

	if ok, _ := Verify("px", h); ok {
		t.Error("wrong password verified")
	}

	if h2, _ := Hash("pw"); h2 == h {
		t.Error("same salt twice")
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, stored := range []string{"nocolon", "zz:zz"} {
		if _, err := Verify("x", stored); err == nil {
			t.Errorf("%q: no error", stored)
		}
	}
}
