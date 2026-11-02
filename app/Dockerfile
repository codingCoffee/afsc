# Production image: a static afsc binary with the admin SPA embedded, on distroless (non-root, no shell).
# Build args without defaults must be set; see docs/ops.md "Container image" for how to look them up.
#   docker build \
#     --build-arg GO_IMAGE_DIGEST=sha256:… --build-arg DISTROLESS_DIGEST=sha256:… --build-arg BUN_SHA256=… \
#     -t afsc:dev .

ARG GO_VERSION=1.27.3
ARG GO_IMAGE_DIGEST
ARG DISTROLESS_DIGEST

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie@${GO_IMAGE_DIGEST} AS client
ARG BUN_VERSION=1.4.4
ARG BUN_SHA256
RUN set -eu; \
  test -n "${BUN_SHA256}" || { echo "set --build-arg BUN_SHA256 (from the release's SHASUMS256.txt)"; exit 1; }; \
  case "$(uname -m)" in x86_64) arch=x64 ;; aarch64) arch=aarch64 ;; *) echo "unsupported build arch"; exit 1 ;; esac; \
  apt-get update; apt-get install -y --no-install-recommends unzip; rm -rf /var/lib/apt/lists/*; \
  curl -fsSLo /tmp/bun.zip "https://github.com/oven-sh/bun/releases/download/bun-v${BUN_VERSION}/bun-linux-${arch}.zip"; \
  echo "${BUN_SHA256}  /tmp/bun.zip" | sha256sum -c -; \
  unzip -q /tmp/bun.zip -d /tmp; install -m 0755 "/tmp/bun-linux-${arch}/bun" /usr/local/bin/bun; rm -rf /tmp/bun*
WORKDIR /src/client
COPY client/package.json client/bun.lock ./
RUN bun install --frozen-lockfile --ignore-scripts
COPY client/ ./
RUN bun run postinstall && bun run generate

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-trixie@${GO_IMAGE_DIGEST} AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
COPY --from=client /src/client/.output/public ./client/.output/public
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  go build -trimpath -tags prod -ldflags "-s -w" -o /out/afsc .

FROM gcr.io/distroless/static-debian12:nonroot@${DISTROLESS_DIGEST}
WORKDIR /srv/afsc
COPY --from=build /out/afsc /usr/local/bin/afsc
COPY themes /srv/afsc/themes
ENV AFSC_THEME_DIR=/srv/afsc/themes/default \
  AFSC_API_HOST=0.0.0.0 \
  AFSC_API_PORT=1381
USER nonroot:nonroot
EXPOSE 1381
ENTRYPOINT ["/usr/local/bin/afsc"]
CMD ["serve"]
