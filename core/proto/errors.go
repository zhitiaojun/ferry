package proto

import "fmt"

// ErrorCode is a protocol error code (§7).
type ErrorCode uint16

// Error codes (§7).
const (
	CodeVersionUnsupported  ErrorCode = 0x0001
	CodeBadMagic            ErrorCode = 0x0002
	CodeProtocolViolation   ErrorCode = 0x0003
	CodeFrameTooLarge       ErrorCode = 0x0004
	CodeUnknownType         ErrorCode = 0x0005
	CodeUnexpectedMessage   ErrorCode = 0x0006
	CodeManifestInvalid     ErrorCode = 0x0007
	CodeChunkHashMismatch   ErrorCode = 0x0008
	CodeFileHashMismatch    ErrorCode = 0x0009
	CodeOffsetInvalid       ErrorCode = 0x000A
	CodeIOError             ErrorCode = 0x000B
	CodeCancelled           ErrorCode = 0x000C
	CodeTimeout             ErrorCode = 0x000D
	CodeUnauthorized        ErrorCode = 0x000E
	CodeRelaySessionMissing ErrorCode = 0x000F
	CodeUnsupportedChunk    ErrorCode = 0x0010
	CodePayloadInvalid      ErrorCode = 0x0011
	CodePathSwitch          ErrorCode = 0x0012
	CodeInternal            ErrorCode = 0xFFFF
)

var codeNames = map[ErrorCode]string{
	CodeVersionUnsupported:  "version_unsupported",
	CodeBadMagic:            "bad_magic",
	CodeProtocolViolation:   "protocol_violation",
	CodeFrameTooLarge:       "frame_too_large",
	CodeUnknownType:         "unknown_type",
	CodeUnexpectedMessage:   "unexpected_message",
	CodeManifestInvalid:     "manifest_invalid",
	CodeChunkHashMismatch:   "chunk_hash_mismatch",
	CodeFileHashMismatch:    "file_hash_mismatch",
	CodeOffsetInvalid:       "offset_invalid",
	CodeIOError:             "io_error",
	CodeCancelled:           "cancelled",
	CodeTimeout:             "timeout",
	CodeUnauthorized:        "unauthorized",
	CodeRelaySessionMissing: "relay_session_not_found",
	CodeUnsupportedChunk:    "unsupported_chunk_size",
	CodePayloadInvalid:      "payload_invalid",
	CodePathSwitch:          "path_switch",
	CodeInternal:            "internal",
}

// String returns the stable machine name of the code (§7).
func (c ErrorCode) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return fmt.Sprintf("error_0x%04X", uint16(c))
}

// Error is a protocol error carrying a wire code and a human-readable message.
// Name is the stable machine name from §7; for local-only failures (such as a
// short header) Code is 0 and Name is set explicitly.
type Error struct {
	Code ErrorCode
	Name string
	Msg  string
}

// NewError builds an Error for a wire error code.
func NewError(code ErrorCode, msg string) *Error {
	return &Error{Code: code, Name: code.String(), Msg: msg}
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Msg == "" {
		return e.Name
	}
	return e.Name + ": " + e.Msg
}

// Is reports whether target is the same protocol error name.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Name == e.Name
}
