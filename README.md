# ub

`ub` is a Go-first Homebrew-compatible package manager.

Inspired by Homebrew, ZeroBrew, and uv.

`ub` does not invoke external package manager executables. It fetches formula metadata and bottles directly from upstream APIs.

Compatibility remapping defaults to:

- `.../ub` instead of `.../brew`
- `.../unbrew` instead of `.../homebrew`

On macOS preferred defaults are:

- prefix: `/opt/ub`
- repository: `/opt/unbrew`

If `/opt` is not writable, `ub` falls back to `/usr/local` and then `$HOME`, while preserving `.../ub` and `.../unbrew` suffixes.

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
