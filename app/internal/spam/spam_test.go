package spam

import (
	"math"
	"net/netip"
	"slices"
	"testing"
	"time"

	"afsc/internal/settings"
)

func TestTokens(t *testing.T) {
	got := Tokens("Buy CHEAP pills at https://Spam.Example.net/x and www.other.example! ok it's", "https://site.example.org/me",
		"Someone@Mail.Example.com")
	want := []string{"and", "buy", "cheap", "domain:other.example", "domain:spam.example.net",
		"from:mail.example.com", "from:site.example.org", "it's", "pills"}
	if !slices.Equal(got, want) {
		t.Fatalf("Tokens = %q, want %q", got, want)
	}
	if got := Tokens("aaaaaaaaaaaaaaaaaaaaa twentycharacterssss", "", ""); !slices.Equal(got, []string{"twentycharacterssss"}) {
		t.Fatalf("length bounds: %q", got)
	}
	if got := Tokens("repeat repeat REPEAT", "", ""); len(got) != 1 {
		t.Fatalf("tokens must be unique: %q", got)
	}
}

func corpus(spam, ham int, tokens map[string]Counts) *Corpus {
	return &Corpus{Messages: Counts{Spam: spam, Ham: ham}, Tokens: tokens}
}

func TestTokenProb(t *testing.T) {
	c := corpus(200, 200, map[string]Counts{
		"rare":    {Spam: 2, Ham: 1}, // 2*1+2 = 4 < 5
		"spammy":  {Spam: 100},
		"hammy":   {Ham: 100},
		"neutral": {Spam: 50, Ham: 25},
	})
	cases := map[string]float64{"rare": 0.4, "unseen": 0.4, "spammy": 0.99, "hammy": 0.01, "neutral": 0.5}
	for tok, want := range cases {
		if got := c.tokenProb(tok); math.Abs(got-want) > 1e-9 {
			t.Errorf("tokenProb(%s) = %v, want %v", tok, got, want)
		}
	}
}

func TestProbabilityUsesFifteenMostInteresting(t *testing.T) {
	tokens := map[string]Counts{}
	var msg []string
	for i := range 15 {
		tok := string(rune('a'+i)) + "spam"
		tokens[tok] = Counts{Spam: 100}
		msg = append(msg, tok)
	}
	for i := range 10 {
		tok := string(rune('a'+i)) + "mild"
		tokens[tok] = Counts{Spam: 30, Ham: 25} // p ≈ 0.375, less interesting than 0.99
		msg = append(msg, tok)
	}
	c := corpus(200, 200, tokens)
	if p := c.Probability(msg); p < 0.999 {
		t.Fatalf("p = %v, want ~1 from the 15 spam tokens", p)
	}
	// two equally strong opposing tokens cancel out
	c = corpus(200, 200, map[string]Counts{"s": {Spam: 100}, "h": {Ham: 100}})
	if p := c.Probability([]string{"s", "h"}); math.Abs(p-0.5) > 1e-9 {
		t.Fatalf("p = %v, want 0.5", p)
	}
	if p := c.Probability(nil); p != 0.5 {
		t.Fatalf("empty message p = %v", p)
	}
}

func TestCorpusActive(t *testing.T) {
	for _, c := range []struct {
		spam, ham int
		want      bool
	}{{199, 500, false}, {500, 199, false}, {200, 200, true}} {
		if got := corpus(c.spam, c.ham, nil).Active(); got != c.want {
			t.Errorf("Active(%d, %d) = %v", c.spam, c.ham, got)
		}
	}
}

func TestClassifierScore(t *testing.T) {
	for p, want := range map[float64]int{0.995: 6, 0.99: 6, 0.98: 3, 0.9: 3, 0.89: 0, 0.5: 0, 0.11: 0, 0.1: -3, 0: -3} {
		if got := ClassifierScore(p); got != want {
			t.Errorf("ClassifierScore(%v) = %d, want %d", p, got, want)
		}
	}
}

func TestMatchBlocklist(t *testing.T) {
	list := []Entry{
		{"keyword", "casino"}, {"domain", "spam.example"}, {"ip", "192.0.2.7"}, {"cidr", "198.51.100.0/24"},
		{"email", "bad@example.com"},
	}
	in := func(mod func(*Input)) Input {
		i := Input{Name: "A", Email: "a@example.org", Body: "hello there"}
		mod(&i)
		return i
	}
	cases := []struct {
		name string
		in   Input
		want int
	}{
		{"clean", in(func(*Input) {}), 0},
		{"keyword in body, any case", in(func(i *Input) { i.Body = "Best CASINO" }), 1},
		{"keyword in name", in(func(i *Input) { i.Name = "casino guy" }), 1},
		{"link subdomain", in(func(i *Input) { i.Body = "see https://www.spam.example/x" }), 1},
		{"lookalike domain", in(func(i *Input) { i.Body = "see https://notspam.example/x" }), 0},
		{"author url domain", in(func(i *Input) { i.URL = "https://spam.example" }), 1},
		{"email domain", in(func(i *Input) { i.Email = "x@spam.example" }), 1},
		{"ip", in(func(i *Input) { i.IP = netip.MustParseAddr("192.0.2.7") }), 1},
		{"mapped ip", in(func(i *Input) { i.IP = netip.MustParseAddr("::ffff:192.0.2.7") }), 1},
		{"cidr", in(func(i *Input) { i.IP = netip.MustParseAddr("198.51.100.200") }), 1},
		{"email exact", in(func(i *Input) { i.Email = "Bad@Example.com" }), 1},
		{"several", in(func(i *Input) { i.Email = "bad@example.com"; i.Body = "casino" }), 2},
	}
	for _, c := range cases {
		got := MatchBlocklist(list, c.in)
		if len(got) != c.want {
			t.Errorf("%s: %d matches %+v, want %d", c.name, len(got), got, c.want)
		}
		for _, r := range got {
			if r.Delta != 5 {
				t.Errorf("%s: delta %d", c.name, r.Delta)
			}
		}
	}
}

func TestNormalizeEntry(t *testing.T) {
	ok := map[[2]string]string{
		{"keyword", " Casino "}:      "casino",
		{"domain", "Spam.Example."}:  "spam.example",
		{"ip", "::ffff:192.0.2.1"}:   "192.0.2.1",
		{"cidr", "198.51.100.9/24"}:  "198.51.100.0/24",
		{"email", "Bad@Example.com"}: "bad@example.com",
	}
	for in, want := range ok {
		if got, err := NormalizeEntry(in[0], in[1]); err != nil || got != want {
			t.Errorf("NormalizeEntry(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range [][2]string{{"domain", "http://x.example"}, {"ip", "300.1.1.1"}, {"cidr", "1.2.3.4"},
		{"email", "Name <a@b.example>"}, {"keyword", "  "}, {"other", "x"}} {
		if _, err := NormalizeEntry(in[0], in[1]); err == nil {
			t.Errorf("NormalizeEntry(%q) accepted", in)
		}
	}
}

func TestLinkReasons(t *testing.T) {
	sum := func(rs []Reason) int {
		n := 0
		for _, r := range rs {
			n += r.Delta
		}
		return n
	}
	for body, want := range map[string]int{
		"no links":                            0,
		"https://a.example https://b.example": 0,
		"https://a.example https://b.example www.c.example":                                    2,
		"http://1.example http://2.example http://3.example http://4.example http://5.example": 6,
	} {
		if got := sum(LinkReasons(Input{Name: "A", Body: body})); got != want {
			t.Errorf("LinkReasons(%q) = %d, want %d", body, got, want)
		}
	}
	if got := sum(LinkReasons(Input{Name: "visit www.x.example", Body: "hi"})); got != 5 {
		t.Errorf("link in name = %d, want 5", got)
	}
}

func TestDecide(t *testing.T) {
	d := settings.Defaults().Discussion
	cases := []struct {
		name    string
		v       Verdict
		trusted bool
		want    string
	}{
		{"hard", Verdict{Hard: "spam", Score: -10}, true, "spam"},
		{"over spam threshold", Verdict{Score: 6}, true, "spam"},
		{"moderation band", Verdict{Score: 2}, true, "pending"},
		{"low, untrusted", Verdict{Score: 0}, false, "pending"},
		{"low, trusted", Verdict{Score: 1}, true, "approved"},
		{"negative, trusted", Verdict{Score: -3}, true, "approved"},
		{"stale token", Verdict{ForcePending: true}, true, "pending"},
	}
	for _, c := range cases {
		if got := Decide(c.v, d, c.trusted); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	d.TrustedAutoApprove = false
	if got := Decide(Verdict{}, d, true); got != "pending" {
		t.Errorf("auto-approve off: %s", got)
	}
}

func TestFormToken(t *testing.T) {
	s, err := NewSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_700_000_000, 0)
	tok := s.FormToken(42, at)
	if id, issued, ok := s.ParseFormToken(tok); !ok || id != 42 || !issued.Equal(at) {
		t.Fatalf("round trip: %d %v %v", id, issued, ok)
	}
	tampered := []byte(tok)
	tampered[3] ^= 1
	if _, _, ok := s.ParseFormToken(string(tampered)); ok {
		t.Fatal("tampered token accepted")
	}
	other, _ := NewSigner([]byte("other"))
	if _, _, ok := other.ParseFormToken(tok); ok {
		t.Fatal("token from another key accepted")
	}
	if _, ok := s.ParseNoticeCookie(tok, at); ok {
		t.Fatal("form token accepted as a cookie")
	}
	c := s.NoticeCookie(7, at.Add(time.Minute))
	if id, ok := s.ParseNoticeCookie(c, at); !ok || id != 7 {
		t.Fatalf("cookie: %d %v", id, ok)
	}
	if _, ok := s.ParseNoticeCookie(c, at.Add(2*time.Minute)); ok {
		t.Fatal("expired cookie accepted")
	}
}

func TestHardRulesShortCircuit(t *testing.T) {
	now := time.Now()
	// a nil db proves the hard rules return before any query
	v, err := Check(t.Context(), nil, Input{Honeypot: "x", Issued: now.Add(-time.Minute), Now: now})
	if err != nil || v.Hard != "spam" || v.Reasons[0].Check != "honeypot" {
		t.Fatalf("honeypot: %+v %v", v, err)
	}
	v, err = Check(t.Context(), nil, Input{Issued: now.Add(-time.Second), Now: now})
	if err != nil || v.Hard != "spam" || v.Reasons[0].Check != "too_fast" {
		t.Fatalf("too fast: %+v %v", v, err)
	}
}
