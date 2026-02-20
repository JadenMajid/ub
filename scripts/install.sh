#!/usr/bin/env bash
set -euo pipefail

REPO="${UB_REPO:-JadenMajid/ub}"
INSTALL_DIR="${UB_INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="ub"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"
TMP_DIR=""

cleanup() {
  if [[ -n "${TMP_DIR:-}" && -d "${TMP_DIR:-}" ]]; then
    rm -rf "$TMP_DIR"
  fi
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1" >&2
    exit 1
  fi
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *)
      echo "error: unsupported operating system: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "error: unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

detect_base_dir() {
  if [[ -n "${UB_BASE_DIR:-}" ]]; then
    printf '%s\n' "$UB_BASE_DIR"
    return 0
  fi

  local home_dir
  home_dir="${HOME:-$(cd ~ && pwd)}"
  local candidates=()
  case "$(uname -s)" in
    Darwin)
      candidates=("/opt" "/usr/local" "$home_dir")
      ;;
    *)
      candidates=("$home_dir/.local" "$home_dir")
      ;;
  esac

  local base
  for base in "${candidates[@]}"; do
    if mkdir -p "$base/ub" >/dev/null 2>&1; then
      printf '%s\n' "$base"
      return 0
    fi
  done

  printf '%s\n' "$home_dir"
}

prompt_yes_no() {
  local prompt="$1"
  local default="$2"
  local reply
  read -r -p "$prompt" reply
  reply="${reply:-$default}"
  case "$reply" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

extract_asset_urls() {
  sed -n 's/.*"browser_download_url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

pick_asset_url() {
  local os="$1"
  local arch="$2"
  local urls="$3"

  local patterns=(
    "/${BIN_NAME}[-_]${os}[-_]${arch}\\.tar\\.gz$"
    "/${BIN_NAME}[-_]${os}[-_]${arch}\\.zip$"
    "/${BIN_NAME}[-_]${os}[-_]${arch}$"
  )

  local pattern
  for pattern in "${patterns[@]}"; do
    local match
    match="$(printf '%s\n' "$urls" | grep -E "$pattern" | head -n 1 || true)"
    if [[ -n "$match" ]]; then
      printf '%s\n' "$match"
      return 0
    fi
  done

  return 1
}

download_release_binary() {
  local os="$1"
  local arch="$2"

  echo "Fetching latest release metadata from ${REPO}..."
  local release_json
  release_json="$(curl -fsSL "$API_URL")"

  local urls
  urls="$(printf '%s\n' "$release_json" | extract_asset_urls)"
  if [[ -z "$urls" ]]; then
    echo "error: no release assets found for ${REPO}" >&2
    exit 1
  fi

  local asset_url
  if ! asset_url="$(pick_asset_url "$os" "$arch" "$urls")"; then
    echo "error: could not find an asset for ${os}/${arch}" >&2
    echo "available assets:" >&2
    printf '  - %s\n' $urls >&2
    exit 1
  fi

  echo "Selected asset: ${asset_url}"

  TMP_DIR="$(mktemp -d)"
  local tmp_dir="$TMP_DIR"

  local archive_path="$tmp_dir/asset"
  curl -fL "$asset_url" -o "$archive_path"

  local extracted="$tmp_dir/extracted"
  mkdir -p "$extracted"

  if [[ "$asset_url" == *.tar.gz ]]; then
    tar -xzf "$archive_path" -C "$extracted"
  elif [[ "$asset_url" == *.zip ]]; then
    require_cmd unzip
    unzip -q "$archive_path" -d "$extracted"
  else
    mv "$archive_path" "$extracted/$BIN_NAME"
  fi

  local found
  found="$(find "$extracted" -type f -name "$BIN_NAME" | head -n 1 || true)"
  if [[ -z "$found" ]]; then
    echo "error: could not find ${BIN_NAME} in downloaded asset" >&2
    exit 1
  fi

  mkdir -p "$INSTALL_DIR"
  local target="$INSTALL_DIR/$BIN_NAME"

  if [[ -f "$target" ]]; then
    if ! prompt_yes_no "${target} already exists. Overwrite? [y/N] " "N"; then
      echo "Install cancelled."
      exit 0
    fi
  fi

  install -m 0755 "$found" "$target"
  echo "Installed ${BIN_NAME} to ${target}"
}

detect_managed_bin_dir() {
  local installed_bin="$1"
  local fallback_base
  fallback_base="$(detect_base_dir)"
  local fallback_dir="$fallback_base/ub/bin"

  if [[ ! -x "$installed_bin" ]]; then
    printf '%s\n' "$fallback_dir"
    return 0
  fi

  local config_output
  config_output="$("$installed_bin" config 2>/dev/null || true)"
  local prefix
  prefix="$(printf '%s\n' "$config_output" | awk -F': ' '/^UB_PREFIX:/ {print $2; exit}')"
  if [[ -n "$prefix" ]]; then
    printf '%s\n' "$prefix/bin"
    return 0
  fi

  printf '%s\n' "$fallback_dir"
}

check_path_and_prompt() {
  local target_dir="$1"
  local managed_bin_dir="$2"
  local block_begin="# >>> ub path >>>"
  local block_end="# <<< ub path <<<"

  local ub_on_path="no"
  if command -v ub >/dev/null 2>&1; then
    ub_on_path="yes"
    echo "ub is on PATH: $(command -v ub)"
  else
    echo "ub is not currently on PATH"
  fi

  local dir_on_path="no"
  case ":$PATH:" in
    *":$target_dir:"*)
      dir_on_path="yes"
      ;;
  esac

  local managed_on_path="no"
  case ":$PATH:" in
    *":$managed_bin_dir:"*)
      managed_on_path="yes"
      ;;
  esac

  if [[ "$dir_on_path" == "yes" && "$managed_on_path" == "yes" ]]; then
    echo "Installed ub directories are already on PATH: $target_dir and $managed_bin_dir"
    return 0
  fi

  if prompt_yes_no "add ub binaries to path with ~/.zshrc? [y/N] " "N"; then
    local line="export PATH=\"$target_dir:$managed_bin_dir:\$PATH\""
    if [[ -f "$HOME/.zshrc" ]] && (grep -Fq "$line" "$HOME/.zshrc" || grep -Fq "$block_begin" "$HOME/.zshrc"); then
      echo "~/.zshrc already contains the ub PATH export line."
    else
      {
        printf '\n%s\n' "$block_begin"
        printf '%s\n' "$line"
        printf '%s\n' "$block_end"
      } >> "$HOME/.zshrc"
      echo "Updated ~/.zshrc"
      echo "Restart your shell or run: source ~/.zshrc"
    fi
  else
    echo "Skipped PATH update."
  fi
}

main() {
  trap cleanup EXIT
  require_cmd curl
  require_cmd tar

  local os
  local arch
  local managed_bin_dir
  os="$(detect_os)"
  arch="$(detect_arch)"

  echo "Installing ${BIN_NAME} (${os}/${arch})"
  download_release_binary "$os" "$arch"
  managed_bin_dir="$(detect_managed_bin_dir "$INSTALL_DIR/$BIN_NAME")"
  check_path_and_prompt "$INSTALL_DIR" "$managed_bin_dir"

  echo "Done."
}

main "$@"
