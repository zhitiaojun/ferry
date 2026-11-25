package proto

import "fmt"

// Capability bits advertised in HELLO / HELLO_ACK (§3.2.1).
const (
	CapResume        uint32 = 1 << 0
	CapMultipath     uint32 = 1 << 1
	CapTLSPin        uint32 = 1 << 2
	CapRelayFallback uint32 = 1 << 3
	CapHotUpgrade    uint32 = 1 << 4
	CapAuth          uint32 = 1 << 5
)

// FIN status values (§3.8).
const (
	FinOK           uint8 = 0
	FinCancelled    uint8 = 1
	FinFailed       uint8 = 2
	FinHashMismatch uint8 = 3
)

// Chunk size bounds (§3.4). DefaultChunkSize is used when MANIFEST omits it.
const (
	MinChunkSize     uint32 = 4096
	MaxChunkSize     uint32 = 262144
	DefaultChunkSize uint32 = 262144
	MaxFiles                = 1024
	MaxNameLen              = 255
	MaxDeviceName           = 64
	MaxErrorMessage         = 512
)

// Hello is the HELLO payload (§3.2).
type Hello struct {
	MinVersion   uint16
	MaxVersion   uint16
	DeviceID     [32]byte
	Nonce        [16]byte
	Capabilities uint32
	Name         string
}

// Encode serializes the HELLO payload.
func (m *Hello) Encode() ([]byte, error) {
	if m.MinVersion == 0 || m.MaxVersion < m.MinVersion {
		return nil, NewError(CodeVersionUnsupported, "invalid version range")
	}
	if len(m.Name) > MaxDeviceName {
		return nil, NewError(CodePayloadInvalid, "device name too long")
	}
	w := &writer{}
	w.u16(m.MinVersion)
	w.u16(m.MaxVersion)
	w.raw(m.DeviceID[:])
	w.raw(m.Nonce[:])
	w.u32(m.Capabilities)
	w.str(m.Name)
	return w.b, nil
}

// ParseHello decodes a HELLO payload.
func ParseHello(p []byte) (*Hello, error) {
	r := &reader{b: p}
	m := &Hello{
		MinVersion: r.u16(),
		MaxVersion: r.u16(),
	}
	copy(m.DeviceID[:], r.array(32))
	copy(m.Nonce[:], r.array(16))
	m.Capabilities = r.u32()
	m.Name = r.str(MaxDeviceName)
	if err := r.done(); err != nil {
		return nil, err
	}
	if m.MinVersion == 0 || m.MaxVersion < m.MinVersion {
		return nil, NewError(CodePayloadInvalid, "invalid version range")
	}
	return m, nil
}

// HelloAck is the HELLO_ACK payload (§3.3).
type HelloAck struct {
	Version      uint16
	DeviceID     [32]byte
	NonceEcho    [16]byte
	Capabilities uint32
	Name         string
}

// Encode serializes the HELLO_ACK payload.
func (m *HelloAck) Encode() ([]byte, error) {
	if len(m.Name) > MaxDeviceName {
		return nil, NewError(CodePayloadInvalid, "device name too long")
	}
	w := &writer{}
	w.u16(m.Version)
	w.raw(m.DeviceID[:])
	w.raw(m.NonceEcho[:])
	w.u32(m.Capabilities)
	w.str(m.Name)
	return w.b, nil
}

// ParseHelloAck decodes a HELLO_ACK payload.
func ParseHelloAck(p []byte) (*HelloAck, error) {
	r := &reader{b: p}
	m := &HelloAck{Version: r.u16()}
	copy(m.DeviceID[:], r.array(32))
	copy(m.NonceEcho[:], r.array(16))
	m.Capabilities = r.u32()
	m.Name = r.str(MaxDeviceName)
	if err := r.done(); err != nil {
		return nil, err
	}
	return m, nil
}

// FileEntry is one file in a MANIFEST.
type FileEntry struct {
	Name    string
	Size    uint64
	MtimeMS int64
	SHA256  [32]byte
}

// Manifest is the MANIFEST payload (§3.4).
type Manifest struct {
	TransferID [16]byte
	ChunkSize  uint32
	Files      []FileEntry
}

// Encode serializes the MANIFEST payload. It does not validate; call Validate.
func (m *Manifest) Encode() ([]byte, error) {
	if len(m.Files) > MaxFiles {
		return nil, NewError(CodeManifestInvalid, "too many files")
	}
	w := &writer{}
	w.raw(m.TransferID[:])
	w.u32(m.ChunkSize)
	w.u32(uint32(len(m.Files)))
	for i := range m.Files {
		f := &m.Files[i]
		if len(f.Name) > MaxNameLen {
			return nil, NewError(CodeManifestInvalid, "file name too long")
		}
		w.str(f.Name)
		w.u64(f.Size)
		w.i64(f.MtimeMS)
		w.raw(f.SHA256[:])
	}
	return w.b, nil
}

// ParseManifest decodes a MANIFEST payload and validates it (§3.4).
func ParseManifest(p []byte) (*Manifest, error) {
	r := &reader{b: p}
	m := &Manifest{}
	copy(m.TransferID[:], r.array(16))
	m.ChunkSize = r.u32()
	count := r.u32()
	if err := r.done(); err != nil {
		return nil, err
	}
	if count == 0 || count > MaxFiles {
		return nil, NewError(CodeManifestInvalid, fmt.Sprintf("file_count %d out of range", count))
	}
	m.Files = make([]FileEntry, 0, count)
	for i := uint32(0); i < count; i++ {
		var f FileEntry
		f.Name = r.str(MaxNameLen)
		f.Size = r.u64()
		f.MtimeMS = r.i64()
		copy(f.SHA256[:], r.array(32))
		if err := r.done(); err != nil {
			return nil, err
		}
		m.Files = append(m.Files, f)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// Validate applies the MANIFEST constraints of §3.4.
func (m *Manifest) Validate() error {
	if m.ChunkSize < MinChunkSize || m.ChunkSize > MaxChunkSize {
		return NewError(CodeUnsupportedChunk, fmt.Sprintf("chunk_size %d out of range", m.ChunkSize))
	}
	if len(m.Files) == 0 || len(m.Files) > MaxFiles {
		return NewError(CodeManifestInvalid, fmt.Sprintf("file_count %d out of range", len(m.Files)))
	}
	for i := range m.Files {
		f := &m.Files[i]
		if err := ValidateFileName(f.Name); err != nil {
			return err
		}
	}
	return nil
}

// ValidateFileName enforces the receiver-side name rules of §3.4.
func ValidateFileName(name string) error {
	if len(name) == 0 || len(name) > MaxNameLen {
		return NewError(CodeManifestInvalid, "file name length out of range")
	}
	if name == "." || name == ".." {
		return NewError(CodeManifestInvalid, "file name must not be . or ..")
	}
	for _, c := range name {
		if c == '/' || c == '\\' || c == 0 {
			return NewError(CodeManifestInvalid, "file name contains a path separator or NUL")
		}
	}
	return nil
}

// Data is a DATA payload (§3.5).
type Data struct {
	TransferID  [16]byte
	FileIndex   uint32
	Offset      uint64
	ChunkSHA256 [32]byte
	Data        []byte
}

// Encode serializes the DATA payload.
func (m *Data) Encode() ([]byte, error) {
	if len(m.Data) == 0 {
		return nil, NewError(CodePayloadInvalid, "empty data chunk")
	}
	w := &writer{}
	w.raw(m.TransferID[:])
	w.u32(m.FileIndex)
	w.u64(m.Offset)
	w.raw(m.ChunkSHA256[:])
	w.raw(m.Data)
	return w.b, nil
}

// ParseData decodes a DATA payload.
func ParseData(p []byte) (*Data, error) {
	const head = 16 + 4 + 8 + 32
	if len(p) <= head {
		return nil, NewError(CodePayloadInvalid, "DATA payload truncated")
	}
	m := &Data{}
	copy(m.TransferID[:], p[0:16])
	m.FileIndex = be32(p[16:20])
	m.Offset = be64(p[20:28])
	copy(m.ChunkSHA256[:], p[28:60])
	m.Data = append([]byte(nil), p[60:]...)
	return m, nil
}

// Ack is the ACK payload (§3.6).
type Ack struct {
	TransferID [16]byte
	FileIndex  uint32
	Offset     uint64
}

// Encode serializes the ACK payload.
func (m *Ack) Encode() ([]byte, error) {
	w := &writer{}
	w.raw(m.TransferID[:])
	w.u32(m.FileIndex)
	w.u64(m.Offset)
	return w.b, nil
}

// ParseAck decodes an ACK payload.
func ParseAck(p []byte) (*Ack, error) {
	if len(p) != 28 {
		return nil, NewError(CodePayloadInvalid, fmt.Sprintf("ACK payload must be 28 bytes, got %d", len(p)))
	}
	m := &Ack{FileIndex: be32(p[16:20]), Offset: be64(p[20:28])}
	copy(m.TransferID[:], p[0:16])
	return m, nil
}

// Resume is the RESUME payload (§3.7).
type Resume struct {
	TransferID [16]byte
	FileIndex  uint32
	Offset     uint64
}

// Encode serializes the RESUME payload.
func (m *Resume) Encode() ([]byte, error) {
	w := &writer{}
	w.raw(m.TransferID[:])
	w.u32(m.FileIndex)
	w.u64(m.Offset)
	return w.b, nil
}

// ParseResume decodes a RESUME payload.
func ParseResume(p []byte) (*Resume, error) {
	if len(p) != 28 {
		return nil, NewError(CodePayloadInvalid, fmt.Sprintf("RESUME payload must be 28 bytes, got %d", len(p)))
	}
	m := &Resume{FileIndex: be32(p[16:20]), Offset: be64(p[20:28])}
	copy(m.TransferID[:], p[0:16])
	return m, nil
}

// Fin is the FIN payload (§3.8).
type Fin struct {
	Status     uint8
	TransferID [16]byte
	Hashes     [][32]byte
}

// Encode serializes the FIN payload.
func (m *Fin) Encode() ([]byte, error) {
	if m.Status > FinHashMismatch {
		return nil, NewError(CodePayloadInvalid, "invalid FIN status")
	}
	if len(m.Hashes) > MaxFiles {
		return nil, NewError(CodeManifestInvalid, "too many file hashes")
	}
	w := &writer{}
	w.u8(m.Status)
	w.raw(m.TransferID[:])
	w.u32(uint32(len(m.Hashes)))
	for i := range m.Hashes {
		w.raw(m.Hashes[i][:])
	}
	return w.b, nil
}

// ParseFin decodes a FIN payload.
func ParseFin(p []byte) (*Fin, error) {
	if len(p) < 21 {
		return nil, NewError(CodePayloadInvalid, "FIN payload truncated")
	}
	m := &Fin{Status: p[0]}
	copy(m.TransferID[:], p[1:17])
	count := be32(p[17:21])
	if count > MaxFiles {
		return nil, NewError(CodeManifestInvalid, fmt.Sprintf("file_count %d out of range", count))
	}
	if len(p) != 21+int(count)*32 {
		return nil, NewError(CodePayloadInvalid, "FIN hash table length mismatch")
	}
	m.Hashes = make([][32]byte, count)
	for i := 0; i < int(count); i++ {
		copy(m.Hashes[i][:], p[21+i*32:])
	}
	return m, nil
}

// ErrorMsg is the ERROR payload (§3.9).
type ErrorMsg struct {
	Code    ErrorCode
	Message string
}

// Encode serializes the ERROR payload.
func (m *ErrorMsg) Encode() ([]byte, error) {
	if len(m.Message) > MaxErrorMessage {
		return nil, NewError(CodePayloadInvalid, "error message too long")
	}
	w := &writer{}
	w.u16(uint16(m.Code))
	w.str(m.Message)
	return w.b, nil
}

// ParseError decodes an ERROR payload.
func ParseError(p []byte) (*ErrorMsg, error) {
	r := &reader{b: p}
	m := &ErrorMsg{Code: ErrorCode(r.u16())}
	m.Message = r.str(MaxErrorMessage)
	if err := r.done(); err != nil {
		return nil, err
	}
	return m, nil
}

// Ping is the PING / PONG payload (§3.10).
type Ping struct {
	Token [8]byte
}

// Encode serializes the PING / PONG payload.
func (m *Ping) Encode() ([]byte, error) {
	w := &writer{}
	w.raw(m.Token[:])
	return w.b, nil
}

// ParsePing decodes a PING / PONG payload.
func ParsePing(p []byte) (*Ping, error) {
	if len(p) != 8 {
		return nil, NewError(CodePayloadInvalid, fmt.Sprintf("PING payload must be 8 bytes, got %d", len(p)))
	}
	m := &Ping{}
	copy(m.Token[:], p)
	return m, nil
}

// AuthMsg kind values for challenge-response session authentication.
const (
	AuthKindChallenge uint8 = 1
	AuthKindProof     uint8 = 2
)

// AuthMsg is the AUTH payload used when both peers negotiate CapAuth.
type AuthMsg struct {
	Kind   uint8
	Nonce  [16]byte
	PubKey [32]byte
	Sig    [64]byte
}

// Encode serializes an AUTH challenge or proof payload.
func (m *AuthMsg) Encode() ([]byte, error) {
	w := &writer{}
	w.u8(m.Kind)
	switch m.Kind {
	case AuthKindChallenge:
		w.raw(m.Nonce[:])
	case AuthKindProof:
		w.raw(m.PubKey[:])
		w.raw(m.Sig[:])
	default:
		return nil, NewError(CodePayloadInvalid, fmt.Sprintf("invalid AUTH kind %d", m.Kind))
	}
	return w.b, nil
}

// ParseAuth decodes an AUTH challenge or proof payload.
func ParseAuth(p []byte) (*AuthMsg, error) {
	if len(p) < 1 {
		return nil, NewError(CodePayloadInvalid, "empty AUTH payload")
	}
	m := &AuthMsg{Kind: p[0]}
	switch m.Kind {
	case AuthKindChallenge:
		if len(p) != 1+16 {
			return nil, NewError(CodePayloadInvalid, fmt.Sprintf("AUTH challenge must be 17 bytes, got %d", len(p)))
		}
		copy(m.Nonce[:], p[1:17])
	case AuthKindProof:
		if len(p) != 1+32+64 {
			return nil, NewError(CodePayloadInvalid, fmt.Sprintf("AUTH proof must be 97 bytes, got %d", len(p)))
		}
		copy(m.PubKey[:], p[1:33])
		copy(m.Sig[:], p[33:97])
	default:
		return nil, NewError(CodePayloadInvalid, fmt.Sprintf("invalid AUTH kind %d", m.Kind))
	}
	return m, nil
}
