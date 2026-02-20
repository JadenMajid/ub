# ub

`ub` is a Go-first package manager.

Inspired by Homebrew, ZeroBrew, and uv.

`ub` does not invoke external package manager executables. It fetches formula metadata and bottles directly from the homebrew API.

On macOS preferred defaults are:

- prefix: `/opt/ub`
- repository: `/opt/unbrew`

If `/opt` is not writable, `ub` falls back to `/usr/local` and then `$HOME`.

## Install script

Use the interactive installer:

```bash
bash ./scripts/install.sh
```

The installer:

- Detects your OS/architecture and downloads the latest release binary from GitHub Releases.
- Installs `ub` to `${UB_INSTALL_DIR:-$HOME/.local/bin}`.
- Checks whether `ub` and the installed ub binaries directory are already on your `PATH`.
- Prompts with: `add ub binaries to path with ~/.zshrc?`

## Uninstall script

Use the interactive uninstaller:

```bash
bash ./scripts/uninstall.sh
```

The uninstaller removes:

- The `ub` binary from `${UB_INSTALL_DIR:-$HOME/.local/bin}` (and active `command -v ub` location if present).
- `ub` PATH entries from `~/.zshrc` (marker block and legacy single-line export).
- Installed `ub` data directories (`ub` and `unbrew`) under common roots (`$HOME`, `/usr/local`, `/opt`, and `UB_BASE_DIR` if set).
- `~/.cache/ub` and `~/.config/ub` if present.

## Release script

Build and publish a GitHub release (cross-compiled binaries + checksums):

```bash
bash ./scripts/release.sh v0.1.0 --generate-notes
```

What it does:

- Runs `go test ./...` (unless `--skip-tests` is set).
- Builds release archives for:
  - `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`
- Produces `dist/<tag>/checksums.txt`.
- Ensures tag exists and pushes it to `origin`.
- Creates (or updates) a GitHub release using `gh`.

Useful options:

- `--notes-file <path>`
- `--draft`
- `--prerelease`
- `--skip-tests`
- `--allow-dirty`

## Test script

Run all tests (unit, integration, and E2E):

```bash
bash ./scripts/test.sh
```

Optional race detector run:

```bash
bash ./scripts/test.sh --race
```

## Wrapper behavior

- `ub install/info/search/list/uninstall/prefix/config/update` are implemented natively in Go.
- Metadata source: `https://formulae.brew.sh/api/formula/*.json`
- Bottle downloads come from URLs provided by the Homebrew formula API.
- Install locations:
  - prefix: `.../ub`
  - repository: `.../unbrew`
  - cellar: `.../ub/Cellar`
  - cache: `.../ub/cache`

Environment overrides:

- `UB_BASE_DIR` to change the root path (default `/opt` on macOS)

Currently implemented native commands:

- `ub install <formula...> [--jobs N]`
- `ub uninstall <formula...>` (`remove` / `rm` aliases)
- `ub list`
- `ub info <formula...>`
- `ub search [query]`
- `ub update`
- `ub prefix [formula]`
- `ub config`

## Prototype MVP scope

- Formula format: JSON files in a tap directory (`<tap>/<name>.json`)
- Dependency resolution: recursive, local tap only
- Execution model: dependency-aware parallel installs using a bounded worker pool
- Fetch/cache: concurrent-safe URL cache with per-source deduplication
- Reliability: 3-attempt download retries with backoff+jitter
- Safety: process-level install lock (`.ub.lock`) and isolated build env per formula
- Install layout: `<root>/<formula>/<version>/INSTALL_RECEIPT.json`
- Commands:
  - `ub mvp-plan <formula...>`
  - `ub mvp-install <formula...> [--jobs N] [--tap DIR] [--root DIR] [--cache DIR]`

## Formula format

```json
{
  "name": "hello",
  "version": "1.0.0",
  "deps": ["libfoo"],
  "source": { "url": "https://example.com/hello.tar.gz", "sha256": "..." },
  "build": { "steps": ["echo building hello"] }
}
```

## Quick start

```bash
mkdir -p taps/core
cat > taps/core/libfoo.json <<'EOF'
{
  "name": "libfoo",
  "version": "1.0.0",
  "build": { "steps": ["echo building libfoo"] }
}
EOF

cat > taps/core/hello.json <<'EOF'
{
  "name": "hello",
  "version": "1.0.0",
  "deps": ["libfoo"],
  "build": { "steps": ["echo building hello"] }
}
EOF

go run ./cmd/ub mvp-plan hello
go run ./cmd/ub mvp-install hello --jobs 4 --cache ./cache
```

## Notes on parallelism

- Jobs run concurrently up to `--jobs` workers.
- Dependencies are strictly enforced; a formula runs only after all prerequisites succeed.
- Independent dependency branches are executed in parallel.

## Benchmarking ub (cold vs warm)

Use the benchmark harness to compare common command timings (`install` and `uninstall`) for `ffmpeg` and `cursor` across cold-cache and warm-cache `ub` runs:

```bash
go run ./cmd/ub-benchmark/main.go --iterations 3
```

Options:

- `--iterations N`: measured runs per case
- `--warmup`: run one unmeasured warmup before timing
- `--ffmpeg` / `--cursor`: include or exclude benchmark targets
- `--json`: machine-readable output with raw timings and speedup ratios

Example JSON output:

```bash
go run ./cmd/ub-benchmark/main.go --iterations 2 --json
```
