#!/usr/bin/env bash
set -euo pipefail

REPO="${UB_REPO:-JadenMajid/ub}"
INSTALL_DIR="${UB_INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="ub"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"

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

  local tmp_dir
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT

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

check_path_and_prompt() {
  local target_dir="$1"
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

  if [[ "$dir_on_path" == "yes" ]]; then
    echo "Installed ub binaries directory is already on PATH: $target_dir"
    return 0
  fi

  if prompt_yes_no "add ub binaries to path with ~/.zshrc? [y/N] " "N"; then
    local line="export PATH=\"$target_dir:\$PATH\""
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
  require_cmd curl
  require_cmd tar

  local os
  local arch
  os="$(detect_os)"
  arch="$(detect_arch)"

  echo "Installing ${BIN_NAME} (${os}/${arch})"
  download_release_binary "$os" "$arch"
  check_path_and_prompt "$INSTALL_DIR"

  echo "Done."
}

main "$@"
