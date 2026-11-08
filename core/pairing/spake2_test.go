package pairing

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func newSID(t *testing.T) [16]byte {
	t.Helper()
	var sid [16]byte
	if _, err := rand.Read(sid[:]); err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestSPAKE2AgreeOnSameCode(t *testing.T) {
	sid := newSID(t)
	a, err := Start(RoleInitiator, "123456", sid)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Start(RoleResponder, "123456", sid)
	if err != nil {
		t.Fatal(err)
	}
	ka, err := a.Finish(b.Message())
	if err != nil {
		t.Fatalf("initiator finish: %v", err)
	}
	kb, err := b.Finish(a.Message())
	if err != nil {
		t.Fatalf("responder finish: %v", err)
	}
	if ka != kb {
		t.Fatal("keys differ for equal codes")
	}
	ta := ConfirmTag(ka, "initiator")
	tb := ConfirmTag(kb, "initiator")
	if !bytes.Equal(ta, tb) {
		t.Fatal("confirm tags differ")
	}
	if bytes.Equal(ConfirmTag(ka, "initiator"), ConfirmTag(ka, "responder")) {
		t.Fatal("confirm labels must be bound into the tag")
	}
}

func TestSPAKE2DifferentCodeDifferentKey(t *testing.T) {
	sid := newSID(t)
	a, _ := Start(RoleInitiator, "123456", sid)
	b, _ := Start(RoleResponder, "654321", sid)
	ka, err := a.Finish(b.Message())
	if err != nil {
		t.Fatal(err)
	}
	kb, err := b.Finish(a.Message())
	if err != nil {
		t.Fatal(err)
	}
	if ka == kb {
		t.Fatal("different codes must not agree")
	}
}

func TestSPAKE2DifferentSessionDifferentKey(t *testing.T) {
	code := "000123"
	sid1, sid2 := newSID(t), newSID(t)
	a, _ := Start(RoleInitiator, code, sid1)
	b, _ := Start(RoleResponder, code, sid1)
	k1, err := a.Finish(b.Message())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := Start(RoleInitiator, code, sid2)
	d, _ := Start(RoleResponder, code, sid2)
	k2, err := c.Finish(d.Message())
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Fatal("session id must be part of the key")
	}
}

func TestSPAKE2RejectsBadShare(t *testing.T) {
	sid := newSID(t)
	a, _ := Start(RoleInitiator, "123456", sid)
	bad := make([]byte, shareSize)
	bad[0] = 4 // 0,0 is not on P-256
	if _, err := a.Finish(bad); err == nil {
		t.Fatal("off-curve point accepted")
	}
	if _, err := a.Finish([]byte{1, 2, 3}); err == nil {
		t.Fatal("short share accepted")
	}
}

func TestStartRejectsShortCode(t *testing.T) {
	sid := newSID(t)
	if _, err := Start(RoleInitiator, "123", sid); err == nil {
		t.Fatal("short code accepted")
	}
	if _, err := Start(Role(9), "123456", sid); err == nil {
		t.Fatal("bad role accepted")
	}
}
