package repoprotocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const verificationSignatureDomain = "dev-plane/verification-attempt/hmac-sha256/v1\x00"

// SignVerificationAttempt authenticates all of the concluded attempt's fields.
// The caller must keep the signing key available only to its trusted verifier.
func SignVerificationAttempt(attempt VerificationAttempt, key []byte) (VerificationAttempt, error) {
	if len(key) < 32 {
		return VerificationAttempt{}, errors.New("verification signing key must have at least 32 bytes")
	}
	if err := attempt.Validate(); err != nil {
		return VerificationAttempt{}, err
	}
	attempt.Signature = ""
	payload, err := json.Marshal(attempt)
	if err != nil { return VerificationAttempt{}, fmt.Errorf("serialize attempt: %w",err) }
	mac := hmac.New(sha256.New,key)
	_,_ = mac.Write([]byte(verificationSignatureDomain))
	_,_ = mac.Write(payload)
	attempt.Signature = "hmac-sha256:"+hex.EncodeToString(mac.Sum(nil))
	return attempt,nil
}

// VerifyVerificationAttemptSignature rejects unsigned, altered, or malformed evidence.
// Structural validation and signatures alone do not prove trusted runner isolation.
func VerifyVerificationAttemptSignature(attempt VerificationAttempt, key []byte) error {
	if len(key) < 32 { return errors.New("verification signing key unavailable or too short") }
	if err := attempt.Validate(); err != nil { return err }
	if !strings.HasPrefix(attempt.Signature,"hmac-sha256:") {
		return errors.New("verification signature missing or unsupported")
	}
	got,err:=hex.DecodeString(strings.TrimPrefix(attempt.Signature,"hmac-sha256:"))
	if err!=nil || len(got)!=sha256.Size { return errors.New("invalid verification signature") }
	expected,err:=SignVerificationAttempt(attempt,key)
	if err!=nil{return err}
	want,_:=hex.DecodeString(strings.TrimPrefix(expected.Signature,"hmac-sha256:"))
	if !hmac.Equal(got,want){return errors.New("verification signature mismatch")}
	return nil
}
