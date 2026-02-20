#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

GO_TEST_FLAGS=("-count=1")

if [[ "${1:-}" == "--race" ]]; then
  GO_TEST_FLAGS+=("-race")
fi

echo "==> Running unit tests"
go test "${GO_TEST_FLAGS[@]}" ./internal/...

echo "==> Running integration tests"
go test "${GO_TEST_FLAGS[@]}" ./cmd/ub -run 'TestUninstallSummaryLines|TestE2E_'

echo "==> Running full suite (includes integration + E2E)"
go test "${GO_TEST_FLAGS[@]}" ./...

echo "All tests passed."
