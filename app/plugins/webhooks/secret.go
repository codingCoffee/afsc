package webhooks

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
)

const secretPrefix = "whsec_"

// ErrSecretUnreadable means the stored secret was sealed with another AFSC_JWT_SECRET.
var ErrSecretUnreadable = errors.New("the endpoint's secret can't be decrypted (AFSC_JWT_SECRET changed?): rotate this endpoint's secret")

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return secretPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func hint(secret string) string { return secret[len(secret)-4:] }

func aead(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts secret with AES-256-GCM, bound to the endpoint ID; the nonce leads the result.
func seal(key []byte, id int64, secret string) ([]byte, error) {
	g, err := aead(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	_, _ = rand.Read(nonce)
	return g.Seal(nonce, nonce, []byte(secret), []byte(strconv.FormatInt(id, 10))), nil
}

func unseal(key []byte, id int64, enc []byte) (string, error) {
	g, err := aead(key)
	if err != nil {
		return "", err
	}
	if len(enc) < g.NonceSize() {
		return "", ErrSecretUnreadable
	}
	b, err := g.Open(nil, enc[:g.NonceSize()], enc[g.NonceSize():], []byte(strconv.FormatInt(id, 10)))
	if err != nil {
		return "", ErrSecretUnreadable
	}
	return string(b), nil
}

// Sign is the X-AFSC-Signature value: t=<unix>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>.
func Sign(secret string, t int64, body []byte) string {
	ts := strconv.FormatInt(t, 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
