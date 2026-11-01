package imgproxy

import (
  "context"
  "encoding/base64"
  "errors"
  "io"
  "net/http"
  "net/http/httptest"
  "strings"
  "testing"
)

func TestSignDocsVector(t *testing.T) {
  // the example from imgproxy's "Signing the URL" docs: key "secret", salt "hello"
  path := "/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png"
  if got := Sign([]byte("secret"), []byte("hello"), path); got != "oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8" {
    t.Errorf("signature %s", got)
  }
}

func TestPath(t *testing.T) {
  c := Config{Bucket: "afsc-media"}
  src := base64.RawURLEncoding.EncodeToString([]byte("s3://afsc-media/o/ab/abcd"))
  cases := []struct {
    name string
    o    Options
    want string
  }{
    {"original size", Options{Format: "webp"}, "/rs:fit:0:0:0/sm:1/" + src + ".webp"},
    {"width", Options{Width: 640, Format: "jpg"}, "/rs:fit:640:0:0/sm:1/" + src + ".jpg"},
    {"crop centre", Options{Width: 150, Height: 150, Crop: true, Format: "png"}, "/rs:fill:150:150:0/g:ce/sm:1/" + src + ".png"},
    {"crop focal", Options{Width: 150, Height: 100, Crop: true, Focal: &[2]float64{0.25, 0.5}, Format: "avif"},
      "/rs:fill:150:100:0/g:fp:0.25:0.5/sm:1/" + src + ".avif"},
    {"focal ignored without crop", Options{Width: 300, Height: 300, Focal: &[2]float64{0.1, 0.1}}, "/rs:fit:300:300:0/sm:1/" + src},
  }
  for _, tc := range cases {
    if got := c.Path("o/ab/abcd", tc.o); got != tc.want {
      t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
    }
  }
}

func TestConfigFromEnv(t *testing.T) {
  key := strings.Repeat("ab", 32)
  cases := []struct {
    name, url, key, salt string
    ok                   bool
  }{
    {"valid", "http://imgproxy:8080/", key, key, true},
    {"no url", "", key, key, false},
    {"bad scheme", "ftp://imgproxy", key, key, false},
    {"short key", "http://imgproxy:8080", "abcd", key, false},
    {"not hex", "http://imgproxy:8080", key, strings.Repeat("zz", 32), false},
  }
  for _, tc := range cases {
    t.Setenv("AFSC_IMGPROXY_URL", tc.url)
    t.Setenv("AFSC_IMGPROXY_KEY", tc.key)
    t.Setenv("AFSC_IMGPROXY_SALT", tc.salt)
    c, err := ConfigFromEnv("afsc-media")
    if (err == nil) != tc.ok {
      t.Errorf("%s: err %v", tc.name, err)
    }
    if tc.ok && c.URL != "http://imgproxy:8080" {
      t.Errorf("%s: url %q", tc.name, c.URL)
    }
  }
}

func TestFetch(t *testing.T) {
  key, salt := []byte(strings.Repeat("k", 32)), []byte(strings.Repeat("s", 32))
  srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    sig, path, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
    if Sign(key, salt, "/"+path) != sig {
      http.Error(w, "bad signature", http.StatusForbidden)
      return
    }
    if strings.Contains(path, "rs:fit:404") {
      http.NotFound(w, r)
      return
    }
    w.Header().Set("Content-Type", "image/webp")
    io.WriteString(w, "IMG")
  }))
  defer srv.Close()

  c := New(Config{URL: srv.URL, Key: key, Salt: salt, Bucket: "b"})
  img, err := c.Fetch(context.Background(), "o/aa/key", Options{Width: 320, Format: "webp"})
  if err != nil {
    t.Fatal(err)
  }
  body, _ := io.ReadAll(img.Body)
  img.Body.Close()
  if string(body) != "IMG" || img.ContentType != "image/webp" {
    t.Errorf("got %q %q", body, img.ContentType)
  }
  if _, err := c.Fetch(context.Background(), "o/aa/key", Options{Width: 404}); !errors.Is(err, ErrNotFound) {
    t.Errorf("404 from imgproxy: got %v, want ErrNotFound", err)
  }
  bad := New(Config{URL: srv.URL, Key: []byte("wrong"), Salt: salt, Bucket: "b"})
  if _, err := bad.Fetch(context.Background(), "o/aa/key", Options{}); err == nil || errors.Is(err, ErrNotFound) {
    t.Errorf("bad signature should be an upstream error, got %v", err)
  }
}
