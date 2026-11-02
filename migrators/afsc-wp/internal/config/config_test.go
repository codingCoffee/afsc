package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "afsc-wp.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	p := write(t, `{"site":{"base_url":"https://blog.example.com"},"uploads_url_path":"wp-content/uploads/"}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "wp" || c.MySQL.TablePrefix != "wp_" || c.UploadsURLPath != "/wp-content/uploads" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Work("x") != filepath.Join(filepath.Dir(p), "work", "x") {
		t.Fatalf("work path %s", c.Work("x"))
	}
	hosts := strings.Join(c.SiteHosts("old.example.com"), ",")
	for _, h := range []string{"blog.example.com", "www.blog.example.com", "old.example.com", "www.old.example.com"} {
		if !strings.Contains(hosts, h) {
			t.Errorf("hosts %s missing %s", hosts, h)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]string{
		"prefix":   `{"site":{"base_url":"https://a.example.com"},"mysql":{"table_prefix":"wp_; DROP"}}`,
		"source":   `{"site":{"base_url":"https://a.example.com"},"source":"WP"}`,
		"base_url": `{"site":{"base_url":"ftp://a.example.com"}}`,
		"plugin":   `{"site":{"base_url":"https://a.example.com"},"plugins":{"nope":{}}}`,
		"mode":     `{"site":{"base_url":"https://a.example.com"},"plugins":{"guest_author":{"mode":"merge"}}}`,
		"unknown":  `{"site":{"base_url":"https://a.example.com"},"typo":1}`,
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestDSNFromEnv(t *testing.T) {
	c, err := Load(write(t, `{"site":{"base_url":"https://a.example.com"},"mysql":{"dsn_env":"AFSC_WP_TEST_DSN_X"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DSN(); err == nil {
		t.Fatal("want error without env")
	}
	t.Setenv("AFSC_WP_TEST_DSN_X", "u:p@tcp(localhost:3306)/wp")
	if d, err := c.DSN(); err != nil || d == "" {
		t.Fatalf("dsn %q %v", d, err)
	}
}
