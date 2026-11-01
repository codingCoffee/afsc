package sanitize

import (
  "strings"
  "testing"
)

// Vectors adapted from the OWASP XSS filter evasion cheat sheet.
var xssCorpus = []string{
  `<script>alert(1)</script>`,
  `<SCRIPT SRC=https://xss.example/xss.js></SCRIPT>`,
  `<IMG SRC="javascript:alert('XSS');">`,
  `<IMG SRC=javascript:alert('XSS')>`,
  `<IMG SRC=JaVaScRiPt:alert('XSS')>`,
  `<IMG SRC=&#106;&#97;&#118;&#97;&#115;&#99;&#114;&#105;&#112;&#116;&#58;&#97;&#108;&#101;&#114;&#116;&#40;&#39;&#88;&#83;&#83;&#39;&#41;>`,
  `<IMG SRC="jav	ascript:alert('XSS');">`,
  `<IMG SRC="jav&#x0A;ascript:alert('XSS');">`,
  `<IMG SRC=" &#14;  javascript:alert('XSS');">`,
  `<a href="javascript&colon;alert(1)">x</a>`,
  `<a href="  JAVASCRIPT:alert(1)">x</a>`,
  `<a href="vbscript:msgbox(1)">x</a>`,
  `<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">x</a>`,
  `<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=">`,
  `<IMG """><SCRIPT>alert("XSS")</SCRIPT>">`,
  `<IMG SRC=# onmouseover="alert('xxs')">`,
  `<IMG SRC=/ onerror="alert(String.fromCharCode(88,83,83))"></img>`,
  `<img src=x onerror=alert(1)//`,
  `<BODY ONLOAD=alert('XSS')>`,
  `<svg/onload=alert('XSS')>`,
  `<svg><script>alert(1)</script></svg>`,
  `<math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`,
  `<STYLE>@import'https://xss.example/xss.css';</STYLE>`,
  `<DIV STYLE="background-image: url(javascript:alert('XSS'))">`,
  `<DIV STYLE="width: expression(alert('XSS'));">`,
  `<p style="color: red; behavior: url(x.htc)">x</p>`,
  `<IFRAME SRC="javascript:alert('XSS');"></IFRAME>`,
  `<iframe src="https://evil.example/embed"></iframe>`,
  `<iframe src="https://www.youtube.com.evil.example/embed/x"></iframe>`,
  `<FRAMESET><FRAME SRC="javascript:alert('XSS');"></FRAMESET>`,
  `<TABLE BACKGROUND="javascript:alert('XSS')">`,
  `<TD BACKGROUND="javascript:alert('XSS')">`,
  `<OBJECT TYPE="text/x-scriptlet" DATA="https://xss.example/scriptlet.html"></OBJECT>`,
  `<EMBED SRC="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=" type="image/svg+xml">`,
  `<BASE HREF="javascript:alert('XSS');//">`,
  `<META HTTP-EQUIV="refresh" CONTENT="0;url=javascript:alert('XSS');">`,
  `<LINK REL="stylesheet" HREF="javascript:alert('XSS');">`,
  `<form action="javascript:alert(1)"><input type=submit></form>`,
  `<button formaction="javascript:alert(1)">x</button>`,
  `<a href="#" onclick="alert(1)">x</a>`,
  `<details open ontoggle=alert(1)>`,
  `<video><source onerror="alert(1)"></video>`,
  `<video poster="javascript:alert(1)"></video>`,
  `<!--<img src="--><img src=x onerror=alert(1)//">`,
  `<noscript><p title="</noscript><img src=x onerror=alert(1)>">`,
  `<template><script>alert(1)</script></template>`,
  `<blockquote cite="javascript:alert(1)">x</blockquote>`,
  `<img srcset="javascript:alert(1) 1x">`,
  `<x onclick=alert(1)>click</x>`,
}

func TestTrustedXSSCorpus(t *testing.T) {
  bad := []string{"<script", "javascript:", "vbscript:", "data:text", "onerror", "onload", "onclick",
    "onmouseover", "ontoggle", "expression(", "url(", "<iframe src=\"https://evil", "<iframe src=\"javascript",
    "<object", "<embed", "<base", "<meta", "<link", "<form", "<input", "<button", "<svg", "<style", "behavior"}
  for _, in := range xssCorpus {
    out, _ := Trusted(in)
    lower := strings.ToLower(out)
    for _, b := range bad {
      if strings.Contains(lower, b) {
        t.Errorf("Trusted(%q) = %q, contains %q", in, out, b)
      }
    }
    if strings.Contains(lower, "<img") && strings.Contains(lower, "svg") {
      t.Errorf("Trusted(%q) = %q, kept an svg image", in, out)
    }
  }
}

func TestTrustedKeepsAllowedMarkup(t *testing.T) {
  cases := []struct{ in, want string }{
    {`<p class="lead">Hi <strong>there</strong></p>`, `<p class="lead">Hi <strong>there</strong></p>`},
    {`<p style="text-align: center; position: fixed">x</p>`, `<p style="text-align: center">x</p>`},
    {`<a href="https://example.com" target="_blank">x</a>`, `<a href="https://example.com" target="_blank" rel="noopener">x</a>`},
    {`<a href="/relative#a">x</a>`, `<a href="/relative#a">x</a>`},
    {`<a href="mailto:user@example.com">x</a>`, `<a href="mailto:user@example.com">x</a>`},
    {`<img src="data:image/png;base64,AAAA" alt="">`, `<img src="data:image/png;base64,AAAA" alt=""/>`},
    {`<font color="red">x</font>`, `x`},
    {`<iframe src="https://www.youtube-nocookie.com/embed/abc"></iframe>`,
      `<iframe src="https://www.youtube-nocookie.com/embed/abc" sandbox="` + IframeSandbox + `"></iframe>`},
    {`<table><tr><td colspan="2">x</td></tr></table>`, `<table><tbody><tr><td colspan="2">x</td></tr></tbody></table>`},
  }
  for _, c := range cases {
    if got, _ := Trusted(c.in); got != c.want {
      t.Errorf("Trusted(%q)\n got %q\nwant %q", c.in, got, c.want)
    }
  }
}

func TestTrustedReportsRemovals(t *testing.T) {
  _, removed := Trusted(`<p onclick="x">a</p><script>b</script>`)
  if len(removed) != 2 {
    t.Fatalf("want 2 removals, got %+v", removed)
  }
  if removed[0].Element != "p" || removed[0].Attribute != "onclick" {
    t.Errorf("unexpected first removal %+v", removed[0])
  }
  if removed[1].Element != "script" {
    t.Errorf("unexpected second removal %+v", removed[1])
  }
}

func TestComment(t *testing.T) {
  got := Comment("see https://example.com/a?b=1. <b>hi</b>\nbye")
  want := `see <a href="https://example.com/a?b=1" rel="nofollow ugc noopener">https://example.com/a?b=1</a>. &lt;b&gt;hi&lt;/b&gt;<br>` + "\nbye"
  if got != want {
    t.Errorf("Comment\n got %q\nwant %q", got, want)
  }
}
