#!/usr/bin/env bash
set -euo pipefail

BIN_NAME="ub"
INSTALL_DIR="${UB_INSTALL_DIR:-$HOME/.local/bin}"
ZSHRC_PATH="$HOME/.zshrc"

UB_PATH_BLOCK_BEGIN="# >>> ub path >>>"
UB_PATH_BLOCK_END="# <<< ub path <<<"

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

remove_file_if_exists() {
  local path="$1"
  if [[ -f "$path" ]]; then
    rm -f "$path"
    echo "Removed file: $path"
  fi
}

remove_dir_if_exists() {
  local path="$1"
  if [[ -d "$path" ]]; then
    rm -rf "$path"
    echo "Removed directory: $path"
  fi
}

remove_zshrc_path_entries() {
  if [[ ! -f "$ZSHRC_PATH" ]]; then
    return 0
  fi

  local tmp
  tmp="$(mktemp)"

  awk -v begin="$UB_PATH_BLOCK_BEGIN" -v end="$UB_PATH_BLOCK_END" '
    $0 == begin { in_block=1; changed=1; next }
    $0 == end { in_block=0; next }
    in_block == 0 { print }
    END { if (changed == 0) {} }
  ' "$ZSHRC_PATH" > "$tmp"

  local legacy_line
  legacy_line="export PATH=\"$INSTALL_DIR:\$PATH\""

  if grep -Fq "$legacy_line" "$tmp"; then
    grep -Fv "$legacy_line" "$tmp" > "$tmp.filtered"
    mv "$tmp.filtered" "$tmp"
  fi

  if ! cmp -s "$ZSHRC_PATH" "$tmp"; then
    mv "$tmp" "$ZSHRC_PATH"
    echo "Updated $ZSHRC_PATH (removed ub PATH entries)"
  else
    rm -f "$tmp"
  fi
}

collect_install_roots() {
  local roots=()

  roots+=("$HOME")
  roots+=("/usr/local")
  roots+=("/opt")

  local env_base="${UB_BASE_DIR:-}"
  if [[ -n "$env_base" ]]; then
    roots+=("$env_base")
  fi

  printf '%s\n' "${roots[@]}" | awk '!seen[$0]++'
}

main() {
  echo "This will remove ub binary, ub PATH entries in ~/.zshrc, and ub install directories."
  if ! prompt_yes_no "Continue uninstall? [y/N] " "N"; then
    echo "Uninstall cancelled."
    exit 0
  fi

  remove_file_if_exists "$INSTALL_DIR/$BIN_NAME"

  local on_path
  on_path="$(command -v "$BIN_NAME" 2>/dev/null || true)"
  if [[ -n "$on_path" ]]; then
    remove_file_if_exists "$on_path"
  fi

  remove_zshrc_path_entries

  while IFS= read -r root; do
    [[ -z "$root" ]] && continue
    remove_dir_if_exists "$root/ub"
    remove_dir_if_exists "$root/unbrew"
  done < <(collect_install_roots)

  remove_dir_if_exists "$HOME/.cache/ub"
  remove_dir_if_exists "$HOME/.config/ub"

  echo "Uninstall complete."
  echo "If your shell session still resolves ub, restart the shell."
}

main "$@"
