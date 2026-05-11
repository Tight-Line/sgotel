package sendgrid

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	// SignatureHeader carries the base64-encoded ECDSA signature.
	SignatureHeader = "X-Twilio-Email-Event-Webhook-Signature"
	// TimestampHeader carries the unix-seconds timestamp the signature was computed over.
	TimestampHeader = "X-Twilio-Email-Event-Webhook-Timestamp"
)

// Verifier checks SendGrid Signed Event Webhook signatures.
//
// SendGrid signs `timestamp || body` with ECDSA P-256 + SHA-256. The public key
// configured in the SendGrid UI is the base64 encoding of a PKIX-format
// SubjectPublicKeyInfo DER blob.
type Verifier struct {
	pub    *ecdsa.PublicKey
	maxAge time.Duration
	now    func() time.Time
}

// NewVerifier parses the public key (base64-encoded PKIX DER) and returns a
// Verifier configured with the given replay window. maxAge <= 0 disables the
// timestamp window check.
func NewVerifier(publicKeyB64 string, maxAge time.Duration) (*Verifier, error) {
	der, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return nil, fmt.Errorf("public key: base64 decode: %w", err)
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("public key: parse PKIX: %w", err)
	}
	ecPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("public key: not an ECDSA key")
	}
	return &Verifier{pub: ecPub, maxAge: maxAge, now: time.Now}, nil
}

// Verify checks the signature over (timestamp || body) and the timestamp window.
// body must be the exact raw request bytes; do not re-encode JSON before calling.
func (v *Verifier) Verify(timestamp, signature string, body []byte) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("timestamp: parse: %w", err)
	}
	if v.maxAge > 0 {
		skew := v.now().Unix() - ts
		max := int64(v.maxAge.Seconds())
		if skew > max {
			return fmt.Errorf("timestamp: too old (%ds > %ds)", skew, max)
		}
		if skew < -max {
			return fmt.Errorf("timestamp: too far in future (%ds)", -skew)
		}
	}
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("signature: base64 decode: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write(body)
	digest := h.Sum(nil)
	if !ecdsa.VerifyASN1(v.pub, digest, sigBytes) {
		return errors.New("signature: verification failed")
	}
	return nil
}
