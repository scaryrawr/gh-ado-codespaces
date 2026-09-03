#!/usr/bin/env bash
set -euo pipefail

# gh-ado-codespaces terminal shim
tool=$(basename "$0")
case "$tool" in
  terminal-browser|tode) ;;
  *) echo "unsupported terminal shim name: $tool" >&2; exit 64 ;;
esac

if ! command -v curl >/dev/null 2>&1; then
  echo "$tool: curl is required to contact gh-ado-codespaces" >&2
  exit 69
fi

cwd=$(pwd -P)
body=$(mktemp)
capable=$(mktemp)
trap 'rm -f "$body" "$capable"' EXIT
socket_dir=$(cd /tmp && pwd -P)

while IFS= read -r socket; do
  status=$(
    curl --silent --max-time 1 \
      --unix-socket "$socket" \
      --output /dev/null \
      --write-out '%{http_code}' \
      "http://localhost/v1/available/$tool"
  ) || continue
  if [[ "$status" == 204 ]]; then
    printf '%s\n' "$socket" >> "$capable"
  fi
done < <(
  find "$socket_dir" -maxdepth 1 -name 'gh-ado-terminal-*.sock' -type s \
    -exec ls -t {} + 2>/dev/null
)

count=$(wc -l < "$capable")
if [[ "$count" -eq 0 ]]; then
  echo "$tool: no active gh-ado-codespaces session has this local tool" >&2
  exit 69
fi
if [[ "$count" -ne 1 ]]; then
  echo "$tool: multiple active gh-ado-codespaces sessions provide this local tool" >&2
  exit 69
fi
IFS= read -r socket < "$capable"

status=$(
    printf '%s\0' "$cwd" "$@" |
      curl --silent --show-error --max-time 610 \
        --unix-socket "$socket" \
        --header 'Content-Type: application/vnd.gh-ado.launch.v1' \
        --request POST \
        --data-binary @- \
        --output "$body" \
        --write-out '%{http_code}' \
        "http://localhost/v1/launch/$tool"
) || {
  echo "$tool: local gh-ado-codespaces launcher stopped responding" >&2
  exit 69
}

if [[ "$status" == 204 ]]; then
  exit 0
fi
cat "$body" >&2
exit 1
