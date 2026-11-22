package pairing

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateCode(t *testing.T) {
	for i := 0; i < 20; i++ {
		c := GenerateCode()
		if !isSixDigits(c) {
			t.Fatalf("GenerateCode() = %q, want 6 ASCII digits", c)
		}
	}
}

func TestBuildAndParsePairURI(t *testing.T) {
	id := strings.Repeat("a1", 32)
	now := time.Unix(1795348000, 0).UTC()
	exp := now.Add(DefaultTTL)

	raw := BuildPairURI(id, "042917", "HyperX 电脑", exp)
	got, err := ParsePairURI(raw, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ParsePairURI valid: %v", err)
	}
	if got.DeviceID != id || got.Code != "042917" || got.Name != "HyperX 电脑" || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("ParsePairURI = %+v, want id=%s code=042917 name=HyperX 电脑 exp=%v", got, id, exp)
	}

	// Expired (>10 min)
	if _, err := ParsePairURI(raw, exp.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "已过期") {
		t.Fatalf("expired ParsePairURI err = %v, want expiry error", err)
	}
}

func TestParsePairURIRejectsInvalid(t *testing.T) {
	now := time.Unix(1795348000, 0).UTC()
	validID := strings.Repeat("b2", 32)
	cases := []string{
		"",
		"https://example.com/pair",
		"ferry://pair?id=short&code=123456&exp=1795348600",
		"ferry://pair?id=" + validID + "&code=12345&exp=1795348600",
		"ferry://pair?id=" + validID + "&code=abcdef&exp=1795348600",
		"ferry://pair?id=" + validID + "&code=123456",
	}
	for _, tc := range cases {
		if _, err := ParsePairURI(tc, now); err == nil {
			t.Errorf("ParsePairURI(%q) succeeded, want error", tc)
		}
	}
}
