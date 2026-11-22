package pairing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultTTL is the lifetime of one-time pairing codes and QR payloads.
const DefaultTTL = 10 * time.Minute

// PairURI holds the parsed fields from a ferry://pair?... QR payload.
type PairURI struct {
	DeviceID  string
	Code      string
	Name      string
	ExpiresAt time.Time
}

// GenerateCode returns a cryptographically random 6-digit pairing code.
func GenerateCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		// Fallback to monotonic time entropy only if CSPRNG fails unexpectedly.
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// BuildPairURI encodes a pairing QR payload with an explicit expiration timestamp.
func BuildPairURI(deviceID, code, name string, exp time.Time) string {
	q := url.Values{}
	q.Set("id", strings.TrimSpace(deviceID))
	q.Set("code", strings.TrimSpace(code))
	q.Set("exp", strconv.FormatInt(exp.UTC().Unix(), 10))
	if name = strings.TrimSpace(name); name != "" {
		q.Set("name", name)
	}
	u := url.URL{
		Scheme:   "ferry",
		Host:     "pair",
		RawQuery: q.Encode(),
	}
	return u.String()
}

// ParsePairURI parses and validates a ferry://pair?... URI against now.
func ParsePairURI(raw string, now time.Time) (PairURI, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "ferry" || u.Host != "pair" {
		return PairURI{}, errors.New("无效的轻渡配对二维码")
	}
	q := u.Query()
	id := strings.ToLower(strings.TrimSpace(q.Get("id")))
	if b, derr := hex.DecodeString(id); derr != nil || len(b) != 32 {
		return PairURI{}, errors.New("配对二维码缺少有效的设备 ID")
	}
	code := strings.TrimSpace(q.Get("code"))
	if !isSixDigits(code) {
		return PairURI{}, errors.New("配对二维码中的 6 位配对码格式无效")
	}
	expSec, err := strconv.ParseInt(strings.TrimSpace(q.Get("exp")), 10, 64)
	if err != nil || expSec <= 0 {
		return PairURI{}, errors.New("配对二维码缺少有效期限")
	}
	exp := time.Unix(expSec, 0).UTC()
	if now.After(exp) {
		return PairURI{}, errors.New("配对码或二维码已过期（超过 10 分钟），请刷新后重试")
	}
	return PairURI{
		DeviceID:  id,
		Code:      code,
		Name:      strings.TrimSpace(q.Get("name")),
		ExpiresAt: exp,
	}, nil
}

func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
