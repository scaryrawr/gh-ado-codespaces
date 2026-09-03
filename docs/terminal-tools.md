# Terminal tools

[← Back to README](../README.md)

`gh ado-codespaces` detects `terminal-browser` and `tode` on the local `PATH` when a session starts. For each detected tool, the extension installs a Codespace command that requests the captured local executable through the session's SSH connection.

The commands open a right split when no split option is present. An explicit `--split` option and its optional `--size` value take precedence.

## Supported commands

| Command | Supported input |
| --- | --- |
| `terminal-browser` | `open` or implicit open, zero or one HTTP, HTTPS, host, or local port target, `--split`, and `--size` |
| `tode` | Zero or one remote path, `--split`, and `--size` |

`terminal-browser` rejects local file targets, `--ssh`, management commands, code-loading options, and unknown flags. `tode` rejects management commands, `--wait`, and every other flag.

The shim converts host targets to explicit HTTPS URLs. It converts `localhost`, host-and-port targets, and bare ports to explicit HTTP URLs. This prevents a Codespace path from selecting a file on the local machine.

`--size` requires `--split`. Split directions are `right`, `left`, `up`, and `down`. Size values range from `0.2` through `0.95`.

An omitted `tode` path uses the physical remote working directory. A relative path resolves from that directory. An absolute path remains an absolute remote path.

## Session routing

Each local launch uses a generated SSH route with no `RemoteForward` entries. The route is separate from the interactive SSH and Herdr session routes, so a terminal tool cannot rebind their reverse forwards.

The extension installs a shim only when it detects the local tool. A managed shim from a terminated session can remain in the Codespace. That shim reports an unavailable local launcher instead of running a remote tool.

If more than one active session for the same Codespace provides a tool, the shim refuses the request. Close the extra session and run the command again. The shim never chooses a session by socket age.

Terminal tools are unavailable when you use `--profile` or `--server-port`. GitHub CLI cannot combine those options with the generated SSH configuration that the local tools need.

## Browser behavior

Terminal shims do not use `BROWSER`, `browser-opener.sh`, or `xdg-open`. Those browser-opening paths retain their existing behavior.
