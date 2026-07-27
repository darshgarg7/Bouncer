#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
export LANG=C

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_root="${BOUNCER_RELEASE_DIRECTORY:-$repository_root/dist/release}"
reports_root="${BOUNCER_REPORT_DIRECTORY:-$repository_root/dist/test-reports}"
version="${BOUNCER_VERSION:-$(git -C "$repository_root" describe --tags --always --dirty)}"
source_date_epoch="${SOURCE_DATE_EPOCH:-$(git -C "$repository_root" show -s --format=%ct HEAD)}"
if [[ -n "${PYTHON:-}" ]]; then
  python_command="$PYTHON"
elif [[ -x "$repository_root/.venv/bin/python" ]]; then
  python_command="$repository_root/.venv/bin/python"
else
  python_command="python3"
fi
commands=(
  bouncer-harness
  bouncer-provider-gate
  bouncer-run
  bouncer-sandbox
  bouncer-verify-log
)
platforms=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
)

mkdir -p "$release_root" "$reports_root"
find "$release_root" -mindepth 1 -maxdepth 1 -type f -delete
find "$reports_root" -mindepth 1 -maxdepth 1 -type f -delete

for platform in "${platforms[@]}"; do
  os="${platform%/*}"
  arch="${platform#*/}"
  archive_name="bouncer-${version}-${os}-${arch}"
  stage_directory="$(mktemp -d "${TMPDIR:-/tmp}/bouncer-release.XXXXXX")"
  for command_name in "${commands[@]}"; do
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
      -trimpath \
      -ldflags="-s -w -buildid=" \
      -o "$stage_directory/$command_name" \
      "$repository_root/cmd/$command_name"
  done
  cp "$repository_root/LICENSE" "$repository_root/README.md" "$stage_directory/"
  if tar --version 2>/dev/null | grep -q "GNU tar"; then
    tar -C "$stage_directory" \
      --sort=name \
      --mtime="@$source_date_epoch" \
      --owner=0 \
      --group=0 \
      --numeric-owner \
      -cf - . | gzip -n > "$release_root/$archive_name.tar.gz"
  else
    # BSD tar lacks GNU's complete metadata-normalization surface. Official
    # release artifacts are built on Linux; this fallback keeps local smoke
    # builds usable while deterministic Go binaries remain byte-stable.
    tar -C "$stage_directory" -cf - . | gzip -n > "$release_root/$archive_name.tar.gz"
  fi
  rm -rf "$stage_directory"
done

(
  cd "$release_root"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz > SHA256SUMS
  else
    shasum -a 256 ./*.tar.gz > SHA256SUMS
  fi
)

go test -json ./... > "$reports_root/go-test.jsonl"
go test -coverprofile="$reports_root/go-coverage.out" ./... >/dev/null
go tool cover -func="$reports_root/go-coverage.out" > "$reports_root/go-coverage.txt"
"$python_command" -m unittest discover -s tests -v > "$reports_root/python-unittest.txt" 2>&1

printf 'release artifacts built in %s\n' "$release_root"
printf 'test reports built in %s\n' "$reports_root"
