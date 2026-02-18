#!/usr/bin/env bash
# xdg-open shim for GitHub Codespaces (SSH environment)
#
# Replaces the real xdg-open and intelligently routes open requests:
#   - URLs  → forwarded via gh-ado browser socket, $BROWSER, VS Code, or silent no-op
#   - Files → opened with an appropriate viewer (chafa, pdftotext, glow, bat, $EDITOR…)
#             in a tmux pane (if available), via VS Code, or inline over SSH.
#
# Anti-recursion: this script never calls "xdg-open" from PATH.
# When delegating to the real binary it uses /usr/bin/xdg-open (hardcoded).

set -euo pipefail

TARGET="${1:-}"

if [[ -z "$TARGET" ]]; then
    echo "Usage: $(basename "$0") <url-or-file>" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# URL handling
# ---------------------------------------------------------------------------

# open_url: try multiple strategies to open a URL, falling back gracefully.
open_url() {
    local url="$1"

    # 1. Try the gh-ado browser socket (gh-ado-codespaces port-forwarding service).
    #    Mirror the exact discovery pattern used by browser-opener.sh.
    if command -v curl &>/dev/null && command -v jq &>/dev/null; then
        local encoded_url
        encoded_url="$(printf %s "$url" | jq -sRr @uri)"

        # Find all sockets, sort newest-first so we prefer the active one.
        local sock
        while IFS= read -r sock; do
            [[ -z "$sock" ]] && continue
            if curl -s --max-time 2 --unix-socket "$sock" \
                    -X POST "http://localhost/open?url=${encoded_url}" \
                    >/dev/null 2>&1; then
                return 0
            fi
        done < <(find /tmp -maxdepth 1 -name "gh-ado-browser-*.sock" -type s \
                     -exec ls -t {} + 2>/dev/null)
    fi

    # 2. If $BROWSER is set, delegate to it.
    if [[ -n "${BROWSER:-}" ]]; then
        exec "$BROWSER" "$url"
    fi

    # 3. Try VS Code remote's `code --open-url`.
    if command -v code &>/dev/null; then
        code --open-url "$url" &>/dev/null && return 0
    fi

    # 4. Try the real xdg-open (hardcoded path to avoid calling ourselves).
    if [[ -x /usr/bin/xdg-open ]]; then
        /usr/bin/xdg-open "$url" &>/dev/null && return 0
    fi

    # 5. Silent no-op — headless environment, nothing else we can do.
    return 0
}

# ---------------------------------------------------------------------------
# File handling
# ---------------------------------------------------------------------------

# detect_viewer: emit the shell command (as a string) to view the given file.
detect_viewer() {
    local file="$1"
    local ext="${file##*.}"
    ext="${ext,,}"  # lowercase

    case "$ext" in
        jpg|jpeg|png|gif|bmp|webp|tiff|svg)
            # Terminal image viewer.
            if command -v chafa &>/dev/null; then
                echo "chafa $(printf '%q' "$file")"
                return
            fi
            ;;
        pdf)
            if command -v pdftotext &>/dev/null; then
                echo "pdftotext $(printf '%q' "$file") - | less"
                return
            elif command -v pdfinfo &>/dev/null; then
                echo "pdfinfo $(printf '%q' "$file")"
                return
            fi
            ;;
        md|markdown)
            if command -v glow &>/dev/null; then
                echo "glow $(printf '%q' "$file")"
                return
            elif command -v bat &>/dev/null; then
                echo "bat $(printf '%q' "$file")"
                return
            elif [[ -n "${EDITOR:-}" ]]; then
                echo "${EDITOR} $(printf '%q' "$file")"
                return
            fi
            ;;
    esac

    # Everything else: use $EDITOR or fall back to vi.
    if [[ -n "${EDITOR:-}" ]]; then
        echo "${EDITOR} $(printf '%q' "$file")"
    else
        echo "vi $(printf '%q' "$file")"
    fi
}

# is_interactive_editor: returns 0 if the viewer command is an editor
# (interactive — no need for a "press enter" prompt after it exits).
is_interactive_editor() {
    local cmd="$1"
    case "$cmd" in
        vi\ *|vim\ *|nvim\ *|nano\ *|emacs\ *|micro\ *|*"$EDITOR"*)
            return 0
            ;;
    esac
    return 1
}

# open_file: open a file using the best available strategy.
open_file() {
    local file="$1"

    if [[ ! -e "$file" ]]; then
        echo "$(basename "$0"): '$file': No such file or directory" >&2
        exit 2
    fi

    local viewer_cmd
    viewer_cmd="$(detect_viewer "$file")"

    if [[ -n "${TMUX:-}" ]]; then
        # Inside a tmux session → open in a vertical split pane.
        if is_interactive_editor "$viewer_cmd"; then
            # Editors are fully interactive; just run them directly.
            tmux split-window -h "$viewer_cmd"
        else
            # Non-interactive viewers (chafa, bat, less…): keep the pane open
            # until the user presses Enter so they can read the output.
            tmux split-window -h "${viewer_cmd}; read -r -p 'Press enter to close...'"
        fi

    elif [[ -z "${SSH_TTY:-}" && -z "${SSH_CONNECTION:-}" ]]; then
        # Not in an SSH session — try graphical/desktop openers first.

        # Try real xdg-open (hardcoded path).
        if [[ -x /usr/bin/xdg-open ]]; then
            /usr/bin/xdg-open "$file" &>/dev/null && return 0
        fi

        # Try VS Code.
        if command -v code &>/dev/null; then
            code "$file" &>/dev/null && return 0
        fi

        # Fall through to inline viewer.
        eval "$viewer_cmd"

    else
        # SSH session without tmux → run viewer inline (blocking).
        eval "$viewer_cmd"
    fi
}

# ---------------------------------------------------------------------------
# Main dispatch
# ---------------------------------------------------------------------------

# Detect whether the target looks like a URL (http, https, mailto, ftp).
if [[ "$TARGET" =~ ^(https?|mailto|ftp):// ]]; then
    open_url "$TARGET"
else
    open_file "$TARGET"
fi
