package proto

import (
	"bytes"
	"testing"
)

func TestAuthMsgRoundTrip(t *testing.T) {
	var chNonce [16]byte
	for i := range chNonce {
		chNonce[i] = byte(i + 1)
	}
	ch := &AuthMsg{Kind: AuthKindChallenge, Nonce: chNonce}
	enc, err := ch.Encode()
	if err != nil {
		t.Fatalf("encode challenge: %v", err)
	}
	if len(enc) != 17 {
		t.Fatalf("challenge len = %d, want 17", len(enc))
	}
	gotAny, err := DecodePayload(TypeAuth, enc)
	if err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	gotCh, ok := gotAny.(*AuthMsg)
	if !ok || gotCh.Kind != AuthKindChallenge || gotCh.Nonce != chNonce {
		t.Fatalf("decoded challenge = %+v, want %+v", gotAny, ch)
	}

	var pub [32]byte
	var sig [64]byte
	for i := range pub {
		pub[i] = byte(0xA0 + i)
	}
	for i := range sig {
		sig[i] = byte(0x10 + i)
	}
	proof := &AuthMsg{Kind: AuthKindProof, PubKey: pub, Sig: sig}
	enc, err = proof.Encode()
	if err != nil {
		t.Fatalf("encode proof: %v", err)
	}
	if len(enc) != 97 {
		t.Fatalf("proof len = %d, want 97", len(enc))
	}
	gotAny, err = DecodePayload(TypeAuth, enc)
	if err != nil {
		t.Fatalf("decode proof: %v", err)
	}
	gotProof, ok := gotAny.(*AuthMsg)
	if !ok || gotProof.Kind != AuthKindProof || !bytes.Equal(gotProof.PubKey[:], pub[:]) || !bytes.Equal(gotProof.Sig[:], sig[:]) {
		t.Fatalf("decoded proof mismatch")
	}
}

func TestAuthMsgRejectsInvalid(t *testing.T) {
	cases := [][]byte{
		nil,
		{AuthKindChallenge, 1, 2, 3},
		{AuthKindProof, 1, 2, 3},
		{99, 1, 2, 3},
	}
	for i, tc := range cases {
		if _, err := ParseAuth(tc); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	if _, err := (&AuthMsg{Kind: 99}).Encode(); err == nil {
		t.Fatal("expected encode error for invalid kind")
	}
}
