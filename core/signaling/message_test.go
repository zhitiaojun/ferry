package signaling

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAndParseRoundTrip(t *testing.T) {
	e, err := Build(TypeSendRequest, "c-1", SendRequest{
		ToDevice:   strings.Repeat("ab", 32),
		SessionID:  "0123456789abcdef0123456789abcdef",
		TransferID: "fedcba9876543210fedcba9876543210",
		Files:      []FileInfo{{Name: "a.bin", Size: 2_100_000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.V != Version || got.Type != TypeSendRequest || got.ID != "c-1" {
		t.Fatalf("envelope = %+v", got)
	}
	if got.Ts == 0 {
		t.Fatal("timestamp was not set")
	}
	body, err := Decode[SendRequest](got)
	if err != nil {
		t.Fatal(err)
	}
	if body.Files[0].Name != "a.bin" || body.Files[0].Size != 2_100_000 {
		t.Fatalf("data = %+v", body)
	}
}

func TestParseRejectsBadEnvelopes(t *testing.T) {
	cases := map[string]string{
		"not json":  `{`,
		"wrong v":   `{"v":2,"type":"ping"}`,
		"no type":   `{"v":1}`,
		"too large": `{"v":1,"type":"ping","data":{"t":1}}` + strings.Repeat(" ", MaxMessage),
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: Parse accepted an invalid envelope", name)
		}
	}
}

func TestBuildNilDataIsEmpty(t *testing.T) {
	e, err := Build(TypePing, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Data) != 0 {
		t.Fatalf("nil data became %s", e.Data)
	}
}

func TestDecodeEmptyData(t *testing.T) {
	e, err := Build(TypePing, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Decode[Ping](e)
	if err != nil || p.T != 0 {
		t.Fatalf("Decode on empty data = %+v, %v", p, err)
	}
}

func TestBuildRejectsUnmarshalableData(t *testing.T) {
	if _, err := Build(TypePing, "", make(chan int)); err == nil {
		t.Fatal("Build accepted a channel payload")
	}
	var buf bytes.Buffer
	_ = buf
}
