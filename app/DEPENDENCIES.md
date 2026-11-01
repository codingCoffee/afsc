# Dependencies

Every third-party dependency, with why it's here. Pin exact versions and check each against the org's approved list
before adding or upgrading.

## Go

| Module | Why |
|---|---|
| `github.com/labstack/echo/v5` | HTTP server and router (build plan §2 decision). |
| `github.com/jackc/pgx/v5` | PostgreSQL driver and pool; `COPY` support for fast imports. |
| `github.com/golang-migrate/migrate/v4` | Schema migrations, embedded in the binary via its `iofs` source. |
| `github.com/urfave/cli/v3` | Subcommands and flags (`serve`, `migrate`, `import`). |
| `github.com/golang-jwt/jwt/v5` | Signing and verifying admin access tokens. |
| `github.com/google/uuid` | User, token and session ids. |
| `github.com/joho/godotenv` | Loads `.env` in local development. |
| `golang.org/x/crypto` | bcrypt password hashing. |
| `golang.org/x/net` | `html` parser for the allowlist sanitizer (`internal/sanitize`); no third-party sanitizer. |
| `github.com/aws/aws-sdk-go-v2` (`aws`, `config`, `service/s3`) | Media originals in S3-compatible storage. Chosen over a hand-written SigV4 signer so credential chains, retries and signing come from a maintained, audited library. Only the S3 service package is used. |

Indirect modules are pulled in by the above and are listed in `go.sum`.

## npm (admin client, `client/`)

Nuxt in SPA mode, Vue, Vue Router, reka-ui (via shadcn-vue), Tailwind CSS, `@vueuse/core`, `@lucide/vue`,
`vue-sonner`, `class-variance-authority`, `@nuxtjs/color-mode`.

`client/package.json` still uses `^` ranges. Per the build plan these should be exact versions, with the lockfile
committed and `npm audit --audit-level=high` (or the bun equivalent) in CI.
