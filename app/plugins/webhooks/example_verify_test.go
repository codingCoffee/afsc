package webhooks_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"afsc/plugins/webhooks"
)

// Verify is the receiver-side check from docs/webhooks.md: pass the raw request body, not re-encoded JSON.
func Verify(secret, header string, body []byte, now time.Time) error {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return errors.New("malformed X-AFSC-Signature")
	}
	if d := now.Sub(time.Unix(t, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return errors.New("timestamp outside the 5 minute tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return errors.New("signature mismatch")
	}
	return nil
}

func TestSignAndVerify(t *testing.T) {
	body := []byte(`{"event":"ping"}`)
	got := webhooks.Sign("whsec_test", 1700000000, body)
	if got != "t=1700000000,v1=aa8efe37b751e71157c508c5ac4acb1e9fe5225db98355dfc00f4b680afbc447" {
		t.Fatalf("signature %s", got)
	}
	now := time.Unix(1700000060, 0)
	if err := Verify("whsec_test", got, body, now); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}
	for name, err := range map[string]error{
		"tampered body": Verify("whsec_test", got, []byte(`{"event":"pong"}`), now),
		"wrong secret":  Verify("whsec_other", got, body, now),
		"stale":         Verify("whsec_test", got, body, now.Add(10*time.Minute)),
		"malformed":     Verify("whsec_test", "v1=abc", body, now),
	} {
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
