// Package imgproxy builds signed imgproxy URLs and fetches processed images over the internal network (§9).
package imgproxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	URL    string // base URL on the private network, no trailing slash
	Key    []byte
	Salt   []byte
	Bucket string // imgproxy reads originals from s3://Bucket/<storage key>
}

// ConfigFromEnv reads AFSC_IMGPROXY_URL, AFSC_IMGPROXY_KEY and AFSC_IMGPROXY_SALT (hex, as imgproxy expects).
func ConfigFromEnv(bucket string) (Config, error) {
	c := Config{Bucket: bucket}
	raw := os.Getenv("AFSC_IMGPROXY_URL")
	u, err := url.Parse(raw)
	if raw == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" {
		return c, errors.New("AFSC_IMGPROXY_URL must be an http(s) URL on the private network, e.g. http://imgproxy:8080")
	}
	c.URL = strings.TrimSuffix(raw, "/")
	if c.Key, err = hexEnv("AFSC_IMGPROXY_KEY"); err != nil {
		return c, err
	}
	if c.Salt, err = hexEnv("AFSC_IMGPROXY_SALT"); err != nil {
		return c, err
	}
	if bucket == "" {
		return c, errors.New("imgproxy: the S3 bucket is not set")
	}
	return c, nil
}

func hexEnv(name string) ([]byte, error) {
	b, err := hex.DecodeString(os.Getenv(name))
	if err != nil || len(b) < 32 {
		return nil, fmt.Errorf("%s must be at least 32 bytes, hex-encoded (openssl rand -hex 32)", name)
	}
	return b, nil
}

// Options describe one processed image. Zero Width and Height keep the original size.
type Options struct {
	Width, Height int
	Crop          bool        // fill the box exactly (cropping), else fit inside it
	Focal         *[2]float64 // x, y in 0..1, used for crops
	Format        string      // output extension: jpg png gif webp avif; "" keeps the source format
}

// Sign returns base64url(HMAC-SHA256(key, salt + path)) without padding.
func Sign(key, salt []byte, path string) string {
	m := hmac.New(sha256.New, key)
	m.Write(salt)
	m.Write([]byte(path))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// Path is the unsigned processing path for a storage key. Images never upscale and metadata is always stripped.
func (c Config) Path(key string, o Options) string {
	mode := "fit"
	if o.Crop {
		mode = "fill"
	}
	p := fmt.Sprintf("/rs:%s:%d:%d:0", mode, o.Width, o.Height)
	if o.Crop {
		if o.Focal != nil {
			p += "/g:fp:" + ftoa(o.Focal[0]) + ":" + ftoa(o.Focal[1])
		} else {
			p += "/g:ce"
		}
	}
	p += "/sm:1/" + base64.RawURLEncoding.EncodeToString([]byte("s3://"+c.Bucket+"/"+key))
	if o.Format != "" {
		p += "." + o.Format
	}
	return p
}

// SignedURL is the signed imgproxy URL for a storage key.
func (c Config) SignedURL(key string, o Options) string {
	p := c.Path(key, o)
	return c.URL + "/" + Sign(c.Key, c.Salt, p) + p
}

// ErrNotFound means imgproxy couldn't process the source (missing or not an image).
var ErrNotFound = errors.New("imgproxy: source not found or not processable")

// MaxBytes caps a processed image; anything bigger is an error rather than an unbounded stream.
const MaxBytes = 50 << 20

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Image is a processed image. The caller closes Body.
type Image struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
}

func (c *Client) Fetch(ctx context.Context, key string, o Options) (*Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.SignedURL(key, o), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("imgproxy: %w", err)
	}
	switch {
	case res.StatusCode == http.StatusOK:
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusUnprocessableEntity:
		res.Body.Close()
		return nil, ErrNotFound
	default:
		res.Body.Close()
		return nil, fmt.Errorf("imgproxy: status %d", res.StatusCode)
	}
	if res.ContentLength > MaxBytes {
		res.Body.Close()
		return nil, fmt.Errorf("imgproxy: response of %d bytes is over the limit", res.ContentLength)
	}
	body := struct {
		io.Reader
		io.Closer
	}{io.LimitReader(res.Body, MaxBytes), res.Body}
	return &Image{Body: body, ContentType: res.Header.Get("Content-Type"), ContentLength: res.ContentLength}, nil
}
