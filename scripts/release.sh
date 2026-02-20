#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/release.sh <tag> [options]

Example:
  scripts/release.sh v0.2.0 --generate-notes

Options:
  --notes-file <path>   Use release notes from file
  --generate-notes      Let GitHub generate release notes (default)
  --draft               Create release as draft
  --prerelease          Mark release as prerelease
  --skip-tests          Skip go test ./...
  --allow-dirty         Allow running with uncommitted changes
  -h, --help            Show this help
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1" >&2
    exit 1
  fi
}

sha256_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  else
    shasum -a 256 "$file" | awk '{print $1}'
  fi
}

if [[ $# -lt 1 ]]; then
  usage
  exit 1
fi

TAG="$1"
shift

NOTES_FILE=""
GENERATE_NOTES="yes"
IS_DRAFT="no"
IS_PRERELEASE="no"
SKIP_TESTS="no"
ALLOW_DIRTY="no"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --notes-file)
      NOTES_FILE="$2"
      GENERATE_NOTES="no"
      shift 2
      ;;
    --generate-notes)
      GENERATE_NOTES="yes"
      shift
      ;;
    --draft)
      IS_DRAFT="yes"
      shift
      ;;
    --prerelease)
      IS_PRERELEASE="yes"
      shift
      ;;
    --skip-tests)
      SKIP_TESTS="yes"
      shift
      ;;
    --allow-dirty)
      ALLOW_DIRTY="yes"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown option: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-].+)?$ ]]; then
  echo "warning: tag '$TAG' does not match semantic version pattern vX.Y.Z" >&2
fi

require_cmd git
require_cmd go
require_cmd gh
require_cmd tar
require_cmd shasum

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if [[ "$ALLOW_DIRTY" != "yes" ]]; then
  if [[ -n "$(git status --porcelain)" ]]; then
    echo "error: working tree is dirty. Commit or stash changes, or use --allow-dirty." >&2
    exit 1
  fi
fi

if [[ "$SKIP_TESTS" != "yes" ]]; then
  echo "==> Running tests"
  go test ./...
fi

echo "==> Preparing dist artifacts"
DIST_DIR="$REPO_ROOT/dist/$TAG"
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

MATRIX=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
)

ASSETS=()

for entry in "${MATRIX[@]}"; do
  GOOS="${entry%% *}"
  GOARCH="${entry##* }"
  NAME="ub-${GOOS}-${GOARCH}"
  BUILD_DIR="$DIST_DIR/$NAME"
  mkdir -p "$BUILD_DIR"

  echo "==> Building $NAME"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/ub" ./cmd/ub/main.go

  ARCHIVE="$DIST_DIR/${NAME}.tar.gz"
  tar -C "$BUILD_DIR" -czf "$ARCHIVE" ub
  ASSETS+=("$ARCHIVE")

done

CHECKSUMS="$DIST_DIR/checksums.txt"
: > "$CHECKSUMS"
for asset in "${ASSETS[@]}"; do
  printf "%s  %s\n" "$(sha256_file "$asset")" "$(basename "$asset")" >> "$CHECKSUMS"
done
ASSETS+=("$CHECKSUMS")

echo "==> Ensuring tag exists locally and remotely"
if ! git rev-parse "$TAG" >/dev/null 2>&1; then
  git tag -a "$TAG" -m "Release $TAG"
fi
git push origin "$TAG"

if gh release view "$TAG" >/dev/null 2>&1; then
  echo "==> Release $TAG already exists; uploading/replacing assets"
  gh release upload "$TAG" "${ASSETS[@]}" --clobber
  echo "Release updated: $TAG"
  exit 0
fi

echo "==> Creating GitHub release $TAG"
CMD=(gh release create "$TAG" "${ASSETS[@]}" --title "$TAG" --verify-tag)
if [[ "$IS_DRAFT" == "yes" ]]; then
  CMD+=(--draft)
fi
if [[ "$IS_PRERELEASE" == "yes" ]]; then
  CMD+=(--prerelease)
fi
if [[ "$GENERATE_NOTES" == "yes" ]]; then
  CMD+=(--generate-notes)
fi
if [[ -n "$NOTES_FILE" ]]; then
  CMD+=(--notes-file "$NOTES_FILE")
fi

"${CMD[@]}"

echo "Release created: $TAG"
