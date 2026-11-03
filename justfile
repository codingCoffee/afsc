set shell := ["bash", "-euo", "pipefail", "-c"]

image := "codingcoffee/afsc"
repo := "codingCoffee/afsc"
platforms := "linux/amd64,linux/arm64"
dist := "dist"

default:
    @just --list

# Cut a release: bump the semver tag, build binaries and the image in docker, push both, publish a GitHub release.
release bump: (_check bump)
    #!/usr/bin/env bash
    set -euo pipefail
    version=$(just _next-version {{ bump }})
    export AFSC_GO_IMAGE_DIGEST="${AFSC_GO_IMAGE_DIGEST:-$(just _digest "golang:$(just _arg GO_VERSION)-trixie")}"
    export AFSC_DISTROLESS_DIGEST="${AFSC_DISTROLESS_DIGEST:-$(just _digest gcr.io/distroless/static-debian12:nonroot)}"
    export AFSC_BUN_SHA256="${AFSC_BUN_SHA256:-$(just _bun-sha)}"
    for v in AFSC_GO_IMAGE_DIGEST AFSC_DISTROLESS_DIGEST AFSC_BUN_SHA256; do
      [[ -n "${!v}" ]] || { echo "could not resolve $v; set it (see app/docs/ops.md)"; exit 1; }
      echo "$v=${!v}"
    done
    echo "releasing ${version}"
    just _binaries "${version}"
    just _image "${version}"
    git tag -a "${version}" -m "${version}"
    git push origin "${version}"
    gh release create "${version}" --repo {{ repo }} --title "${version}" --generate-notes {{ dist }}/*.tar.gz {{ dist }}/checksums.txt

_check bump:
    #!/usr/bin/env bash
    set -euo pipefail
    [[ "{{ bump }}" =~ ^(patch|minor|major)$ ]] || { echo "usage: just release {patch|minor|major}"; exit 1; }
    : "${GITHUB_TOKEN:?GITHUB_TOKEN must be set}"
    [[ "$(git rev-parse --abbrev-ref HEAD)" == "main" ]] || { echo "release from main"; exit 1; }
    [[ -z "$(git status --porcelain --untracked-files=no)" ]] || { echo "working tree has uncommitted changes"; exit 1; }
    git fetch --tags origin
    [[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || { echo "HEAD is not origin/main"; exit 1; }

_next-version bump:
    #!/usr/bin/env bash
    set -euo pipefail
    last=$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | head -n1)
    IFS=. read -r major minor patch <<< "${last:-v0.0.0}"
    major=${major#v}
    case "{{ bump }}" in
      major) major=$((major + 1)); minor=0; patch=0 ;;
      minor) minor=$((minor + 1)); patch=0 ;;
      patch) patch=$((patch + 1)) ;;
    esac
    echo "v${major}.${minor}.${patch}"

_arg name:
    @sed -n 's/^ARG {{ name }}=//p' app/Dockerfile

_digest ref:
    @docker buildx imagetools inspect {{ ref }} | awk '/^Digest:/ {print $2; exit}'

_bun-sha:
    #!/usr/bin/env bash
    set -euo pipefail
    case "$(uname -m)" in x86_64) arch=x64 ;; aarch64|arm64) arch=aarch64 ;; *) echo "unsupported arch" >&2; exit 1 ;; esac
    curl -fsSL "https://github.com/oven-sh/bun/releases/download/bun-v$(just _arg BUN_VERSION)/SHASUMS256.txt" \
      | awk -v f="bun-linux-${arch}.zip" '$2 == f {print $1}'

_build-args version:
    @echo "--build-arg GO_IMAGE_DIGEST=$AFSC_GO_IMAGE_DIGEST --build-arg DISTROLESS_DIGEST=$AFSC_DISTROLESS_DIGEST --build-arg BUN_SHA256=$AFSC_BUN_SHA256 --build-arg VERSION={{ version }}"

_binaries version:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf {{ dist }} && mkdir -p {{ dist }}/out
    docker buildx build $(just _build-args {{ version }}) --platform {{ platforms }} \
      --target binary --output type=local,dest={{ dist }}/out app
    for dir in {{ dist }}/out/*/; do
      plat=$(basename "$dir")
      name="afsc_{{ version }}_${plat}"
      mkdir -p "{{ dist }}/${name}"
      cp "${dir}afsc" "{{ dist }}/${name}/afsc"
      cp -r app/themes "{{ dist }}/${name}/themes"
      tar -C {{ dist }} -czf "{{ dist }}/${name}.tar.gz" "${name}"
    done
    (cd {{ dist }} && sha256sum *.tar.gz > checksums.txt)

_image version:
    #!/usr/bin/env bash
    set -euo pipefail
    v="{{ version }}"
    docker buildx build $(just _build-args "$v") --platform {{ platforms }} \
      -t {{ image }}:"${v#v}" -t {{ image }}:latest --push app
