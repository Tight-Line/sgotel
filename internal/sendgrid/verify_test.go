package sendgrid

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"strconv"
	"testing"
	"time"
)

type testKey struct {
	priv      *ecdsa.PrivateKey
	publicB64 string
}

func newTestKey(t *testing.T) testKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pubkey: %v", err)
	}
	return testKey{priv: priv, publicB64: base64.StdEncoding.EncodeToString(pubDER)}
}

func (k testKey) sign(t *testing.T, timestamp string, body []byte) string {
	t.Helper()
	h := sha256.New()
	h.Write([]byte(timestamp))
	h.Write(body)
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, h.Sum(nil))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func TestVerifier_Valid(t *testing.T) {
	k := newTestKey(t)
	v, err := NewVerifier(k.publicB64, 5*time.Minute)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	v.now = func() time.Time { return time.Unix(1700000060, 0) }

	body := []byte(`[{"event":"delivered","email":"x@y.com"}]`)
	ts := "1700000000"
	sig := k.sign(t, ts, body)

	if err := v.Verify(ts, sig, body); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifier_TamperedBody(t *testing.T) {
	k := newTestKey(t)
	v, _ := NewVerifier(k.publicB64, 0)
	body := []byte(`[{"event":"delivered"}]`)
	ts := "1700000000"
	sig := k.sign(t, ts, body)

	tampered := []byte(`[{"event":"bounce"}]`)
	if err := v.Verify(ts, sig, tampered); err == nil {
		t.Fatal("want error on tampered body")
	}
}

func TestVerifier_WrongKey(t *testing.T) {
	signer := newTestKey(t)
	verifierKey := newTestKey(t)
	v, _ := NewVerifier(verifierKey.publicB64, 0)
	body := []byte(`[{"event":"delivered"}]`)
	ts := "1700000000"
	sig := signer.sign(t, ts, body)

	if err := v.Verify(ts, sig, body); err == nil {
		t.Fatal("want error verifying with wrong key")
	}
}

func TestVerifier_TimestampTooOld(t *testing.T) {
	k := newTestKey(t)
	v, _ := NewVerifier(k.publicB64, 5*time.Minute)
	v.now = func() time.Time { return time.Unix(1700001000, 0) } // +1000s

	body := []byte(`[{"event":"delivered"}]`)
	ts := "1700000000"
	sig := k.sign(t, ts, body)

	if err := v.Verify(ts, sig, body); err == nil {
		t.Fatal("want error on too-old timestamp")
	}
}

func TestVerifier_TimestampTooFuture(t *testing.T) {
	k := newTestKey(t)
	v, _ := NewVerifier(k.publicB64, 5*time.Minute)
	v.now = func() time.Time { return time.Unix(1700000000, 0) }

	body := []byte(`[{"event":"delivered"}]`)
	ts := strconv.FormatInt(1700001000, 10) // +1000s in the future
	sig := k.sign(t, ts, body)

	if err := v.Verify(ts, sig, body); err == nil {
		t.Fatal("want error on future timestamp")
	}
}

func TestVerifier_MaxAgeDisabled(t *testing.T) {
	k := newTestKey(t)
	v, _ := NewVerifier(k.publicB64, 0)
	v.now = func() time.Time { return time.Unix(9999999999, 0) }

	body := []byte(`[{"event":"delivered"}]`)
	ts := "1"
	sig := k.sign(t, ts, body)

	if err := v.Verify(ts, sig, body); err != nil {
		t.Fatalf("verify with maxAge=0 should ignore timestamp: %v", err)
	}
}

func TestVerifier_BadPublicKey(t *testing.T) {
	if _, err := NewVerifier("not-base64-!!!", 0); err == nil {
		t.Fatal("want error on bad base64 public key")
	}
	if _, err := NewVerifier(base64.StdEncoding.EncodeToString([]byte("garbage")), 0); err == nil {
		t.Fatal("want error on bad PKIX public key")
	}
}

func TestVerifier_BadSignatureEncoding(t *testing.T) {
	k := newTestKey(t)
	v, _ := NewVerifier(k.publicB64, 0)
	if err := v.Verify("1700000000", "not-base64-!!!", []byte("{}")); err == nil {
		t.Fatal("want error on bad base64 signature")
	}
}

func TestVerifier_BadTimestamp(t *testing.T) {
	k := newTestKey(t)
	v, _ := NewVerifier(k.publicB64, time.Minute)
	body := []byte("{}")
	if err := v.Verify("not-a-number", "AAAA", body); err == nil {
		t.Fatal("want error on non-numeric timestamp")
	}
}
