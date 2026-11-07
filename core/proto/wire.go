package proto

// reader is a bounds-checked big-endian payload reader. Once an error occurs
// it is latched and all following reads return zero values.
type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) fail(msg string) {
	if r.err == nil {
		r.err = NewError(CodePayloadInvalid, msg)
	}
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.off+n > len(r.b) {
		r.fail("payload truncated")
		return nil
	}
	v := r.b[r.off : r.off+n]
	r.off += n
	return v
}

func (r *reader) u8() uint8 {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return uint16(b[0])<<8 | uint16(b[1])
}

func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func (r *reader) i64() int64 { return int64(r.u64()) }

func (r *reader) array(n int) []byte {
	b := r.take(n)
	if b == nil {
		return nil
	}
	out := make([]byte, n)
	copy(out, b)
	return out
}

func (r *reader) str(max int) string {
	n := int(r.u16())
	if r.err != nil {
		return ""
	}
	if n > max {
		r.fail("string too long")
		return ""
	}
	return string(r.take(n))
}

func (r *reader) done() error { return r.err }

// writer is a big-endian payload writer.
type writer struct {
	b []byte
}

func (w *writer) u8(v uint8) { w.b = append(w.b, v) }

func (w *writer) u16(v uint16) { w.b = append(w.b, byte(v>>8), byte(v)) }

func (w *writer) u32(v uint32) {
	w.b = append(w.b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *writer) u64(v uint64) {
	w.b = append(w.b,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *writer) i64(v int64) { w.u64(uint64(v)) }

func (w *writer) raw(b []byte) { w.b = append(w.b, b...) }

func (w *writer) str(s string) {
	w.u16(uint16(len(s)))
	w.b = append(w.b, s...)
}
