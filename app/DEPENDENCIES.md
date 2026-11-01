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

| `golang.org/x/image` | `webp.DecodeConfig` for upload dimensions (`internal/media/dims.go`); the stdlib has no WebP decoder. Only the `webp` package is used. |
| `github.com/aws/smithy-go` | Part of the AWS SDK (already in the module graph); used directly to recognise S3 `InvalidRange` errors so media Range requests get a 416. |

Indirect modules are pulled in by the above and are listed in `go.sum`.

## Containers (docker-compose)

| Image | Why |
|---|---|
| `ghcr.io/imgproxy/imgproxy` (pinned tag) | Resizes and re-encodes images for `/m/` and legacy upload paths (build plan §9). Private network only; see `docs/ops.md`. |

## npm (admin client, `client/`)

Nuxt in SPA mode, Vue, Vue Router, reka-ui (via shadcn-vue), Tailwind CSS, `@vueuse/core`, `@lucide/vue`,
`vue-sonner`, `class-variance-authority`, `@nuxtjs/color-mode`.

`client/package.json` still uses `^` ranges. Per the build plan these should be exact versions, with the lockfile
committed and `npm audit --audit-level=high` (or the bun equivalent) in CI.

### Editor (M3d, exact pins, MIT, no Tiptap Pro)

| Package | Why |
|---|---|
| `@tiptap/core`, `@tiptap/pm`, `@tiptap/vue-3` 3.32.0 | ProseMirror editor core, its bundled ProseMirror modules and the Vue bindings (node views). `@tiptap/core` is imported directly by `editor/` so the bun test runs without Vue. |
| `@tiptap/starter-kit` 3.32.0 | Paragraph, headings, lists, marks, history and cursors in one package. |
| `@tiptap/extension-link`, `@tiptap/extension-list` 3.32.0 | Extended so the link mark and ordered list/list item match the Go schema exactly (StarterKit's copies add `class` and `type` attrs). |
| `@tiptap/extension-text-align`, `@tiptap/extension-text-style`, `@tiptap/extension-subscript`, `@tiptap/extension-superscript` 3.32.0 | Alignment, the single `textStyle` mark (color, background) and sub/superscript from the content schema. |
| `@tiptap/extension-table`, `@tiptap/extension-details` 3.32.0 | Table and details nodes from the content schema. |
| `@tiptap/extensions` 3.32.0 | Placeholder and CharacterCount. |
| `@happy-dom/global-registrator` 21.0.3 (dev) | DOM for the editor's `bun test` (schema parity and round trips). |
