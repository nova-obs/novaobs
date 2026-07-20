package alerting

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	platformrbac "novaapm/internal/platform/rbac"
)

const testReceiptTTL = 10 * time.Minute

type TestReceiptSigner interface {
	Issue(subject platformrbac.Subject, inputHash string, testedAt time.Time) (string, error)
	Verify(subject platformrbac.Subject, inputHash string, token string, now time.Time) error
}

type HMACTestReceiptSigner struct{ key []byte }

type testReceiptPayload struct {
	SubjectID   string `json:"sub"`
	SubjectType string `json:"sub_type"`
	InputHash   string `json:"input_hash"`
	ExpiresAt   int64  `json:"exp"`
}

func NewHMACTestReceiptSigner(key []byte) HMACTestReceiptSigner {
	return HMACTestReceiptSigner{key: append([]byte(nil), key...)}
}

func (s HMACTestReceiptSigner) Issue(subject platformrbac.Subject, inputHash string, testedAt time.Time) (string, error) {
	payload, err := json.Marshal(testReceiptPayload{SubjectID: subject.ID, SubjectType: subject.Type, InputHash: inputHash, ExpiresAt: testedAt.Add(testReceiptTTL).Unix()})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.sign([]byte(encoded))), nil
}

func (s HMACTestReceiptSigner) Verify(subject platformrbac.Subject, inputHash string, token string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(s.key) < 32 {
		return ErrTestRequired
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, s.sign([]byte(parts[0]))) {
		return ErrTestRequired
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrTestRequired
	}
	var payload testReceiptPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return ErrTestRequired
	}
	if payload.SubjectID != subject.ID || payload.SubjectType != subject.Type || payload.InputHash != inputHash || now.Unix() > payload.ExpiresAt {
		return ErrTestRequired
	}
	return nil
}

func (s HMACTestReceiptSigner) sign(value []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}
