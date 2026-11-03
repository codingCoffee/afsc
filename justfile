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
    for v in AFSC_GO_IMAGE_DIGEST AFSC_DISTROLESS_DIGEST AFSC_BUN_SHA256; do
      [[ -n "${!v:-}" ]] || { echo "$v must be set (see app/docs/ops.md)"; exit 1; }
    done
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
