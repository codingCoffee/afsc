package spam

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"
)

// Signer signs comment form tokens (post id + issued-at, §7.3) and the commenter's pending-notice cookie.
type Signer struct{ key []byte }

// NewSigner derives the comment key from AFSC_JWT_SECRET. An empty secret gets a random per-process key.
func NewSigner(jwtSecret []byte) (*Signer, error) {
	if len(jwtSecret) == 0 {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		return &Signer{key: b}, nil
	}
	k, err := hkdf.Key(sha256.New, jwtSecret, nil, "afsc-comment", 32)
	if err != nil {
		return nil, err
	}
	return &Signer{key: k}, nil
}

func (s *Signer) mac(kind byte, msg []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte{kind})
	m.Write(msg)
	return m.Sum(nil)
}

func (s *Signer) sign(kind byte, a, b int64) string {
	msg := make([]byte, 16)
	binary.BigEndian.PutUint64(msg[0:], uint64(a))
	binary.BigEndian.PutUint64(msg[8:], uint64(b))
	return base64.RawURLEncoding.EncodeToString(append(msg, s.mac(kind, msg)...))
}

func (s *Signer) parse(kind byte, tok string) (a, b int64, ok bool) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 16+sha256.Size || !hmac.Equal(raw[16:], s.mac(kind, raw[:16])) {
		return 0, 0, false
	}
	return int64(binary.BigEndian.Uint64(raw[0:8])), int64(binary.BigEndian.Uint64(raw[8:16])), true
}

const (
	kindForm   = 'f'
	kindCookie = 'c'
)

// FormToken is the signed token the comment form carries.
func (s *Signer) FormToken(postID int64, issued time.Time) string {
	return s.sign(kindForm, postID, issued.Unix())
}

// ParseFormToken returns the post id and issue time of a valid token.
func (s *Signer) ParseFormToken(tok string) (postID int64, issued time.Time, ok bool) {
	id, at, ok := s.parse(kindForm, tok)
	return id, time.Unix(at, 0), ok
}

// NoticeCookie is the value of the afsc_comment cookie: the comment id until exp.
func (s *Signer) NoticeCookie(commentID int64, exp time.Time) string {
	return s.sign(kindCookie, commentID, exp.Unix())
}

// ParseNoticeCookie returns the comment id of an unexpired cookie value.
func (s *Signer) ParseNoticeCookie(v string, now time.Time) (int64, bool) {
	id, exp, ok := s.parse(kindCookie, v)
	if !ok || now.Unix() > exp {
		return 0, false
	}
	return id, true
}
