# Dependencies

Everything not listed here is the Go standard library or AFSC itself (`replace afsc => ../../app`).

| Module | Why | Notes |
|---|---|---|
| `github.com/go-sql-driver/mysql` | Reads the local WordPress restore (`inventory`, `extract`). | Pre-approved by the migration plan §0.6. Pure Go, one transitive dependency (`filippo.io/edwards25519`). Only used read-only with parameterized queries. |
| `github.com/urfave/cli/v3` | Command-line parsing. | Already in AFSC's graph. |
| `golang.org/x/net/html` | HTML parsing for content conversion, crawling and verification. | Already in AFSC's graph. |
| `golang.org/x/image` | WebP dimensions in the uploads scan and the media check. | Already in AFSC's graph. |
| `github.com/jackc/pgx/v5` | The gated end-to-end test only (imports into the AFSC test database). | Already in AFSC's graph. |

**Declined:** `github.com/chromedp/chromedp`. `verify visual` compares screenshot directories captured elsewhere,
using a stdlib pixel diff.
