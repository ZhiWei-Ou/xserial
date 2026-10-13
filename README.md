<p align="center">
  <img src="assets/xserial-logo.png" width="140" alt="xserial logo">
</p>

<h1 align="center">xserial</h1>

<p align="center">
  <strong>🤖 An AI-powered serial terminal.</strong><br>
  Connect Codex and other coding agents to your devices through MCP.
</p>

<p align="center">
  📦 Single executable, no runtime dependencies.<br>
  Linux · macOS · Windows
</p>

<p align="center">
  <a href="https://github.com/ZhiWei-Ou/xserial/releases/latest"><img src="https://img.shields.io/github/v/release/ZhiWei-Ou/xserial" alt="Latest release"></a>
  <a href="https://github.com/ZhiWei-Ou/xserial/actions/workflows/ci.yml"><img src="https://github.com/ZhiWei-Ou/xserial/actions/workflows/ci.yml/badge.svg" alt="Build status"></a>
  <a href="https://github.com/ZhiWei-Ou/xserial/stargazers"><img src="https://img.shields.io/github/stars/ZhiWei-Ou/xserial" alt="GitHub stars"></a>
  <a href="https://github.com/ZhiWei-Ou/xserial/releases"><img src="https://img.shields.io/github/downloads/ZhiWei-Ou/xserial/total" alt="Release downloads"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT license"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick Start</a> ·
  <a href="#connect-a-coding-agent-via-mcp">MCP</a> ·
  <a href="#documentation">Docs</a> ·
  <a href="https://github.com/ZhiWei-Ou/xserial/issues">Issues</a>
</p>

<p align="center">
  English · <a href="README_zh.md">简体中文</a>
</p>

<p align="center"><strong>🎬 Codex operating a device through xserial MCP</strong></p>

https://github.com/user-attachments/assets/0abb2e41-440a-4888-a991-1f1b4079d223

## Features

- 🤖 **AI via MCP** — Read device output and send exact bytes over stdio.
- 🖥️ **Serial terminal** — RawUI, full-screen TUI, Hexdump, reconnection, logs, and YMODEM.
- 🔬 **Binary debugging** — Hex editing, field inspection, checksums, framing, and command favorites.
- 📝 **Offline analysis** — Record, replay, search, mark, and export traffic.

## Quick Start

Download the archive for your OS and CPU architecture from [Releases](https://github.com/ZhiWei-Ou/xserial/releases/latest), extract it, and add the executable's folder to your `PATH`. Linux, macOS, and Windows builds are available for amd64 and arm64.

```bash
xserial list
xserial /dev/ttyUSB0
```

On macOS, use a port such as `/dev/cu.usbserial-0001`; on Windows, use `COM3`. Press `Ctrl-P h` for help or `Ctrl-P q` to quit.

You can also build a source checkout with native Go: `go build ./cmd/xserial`.

## Usage

### Connect a coding agent via MCP

Keep your serial terminal running, then configure your agent to launch `xserial mcp`
as a stdio subprocess. For Codex CLI:

```bash
codex mcp add xserial -- xserial mcp
```

Claude Code CLI:

```bash
claude mcp add --transport stdio --scope user xserial -- xserial mcp
```

`--scope user` makes the Claude configuration available across projects.
Check the registered server with `codex mcp list` or `claude mcp get xserial`.

The agent launches the stdio process, which automatically starts or reuses the
local session daemon. Use an absolute executable path if `xserial` is not on the
agent's PATH. No separate MCP terminal or HTTP endpoint is needed.

Upgrading an existing setup? Stop the old daemon with the old executable before
replacing it, restart serial terminals, and update the client configuration.
See [migration steps](docs/mcp.md#upgrade-an-existing-mcp-configuration).

| Tool | Purpose |
| --- | --- |
| `serial_status` | Find connected sessions |
| `serial_read` | Read output with continuation cursors |
| `serial_send` | Send text, Hex, or base64 bytes |

See the [MCP guide](docs/mcp.md) for setup.

### YMODEM transfers

In the default terminal, press `Ctrl-P Ctrl-U` to upload a file or `Ctrl-P Ctrl-D` to download into the current directory. In the upload prompt, `Tab` completes file and directory paths; press it again to list ambiguous matches. Use arrow keys, Home/End, and Backspace/Delete to edit, or `Ctrl-U` to clear the text before the cursor. Relative paths, `~/`, and paths containing spaces (optionally quoted) are supported. Press Enter to start or Esc to cancel.

### More commands

| Task | Command |
| --- | --- |
| Full-screen console (Beta) | `xserial /dev/ttyUSB0 --TUI` |
| Binary workbench | `xserial /dev/ttyUSB0 --workbench` |
| Hexdump with timestamps | `xserial /dev/ttyUSB0 --hexdump --time` |
| Receive log | `xserial /dev/ttyUSB0 --log device.log` |
| Record / replay | `xserial /dev/ttyUSB0 --record session.xsr` / `xserial replay session.xsr` |
| Export | `xserial export session.xsr -o excerpt.txt` |

Connection syntax: `xserial <port> [baud,data-bits,parity,stop-bits]`; defaults: `115200,8,N,1`. Trailing fields can be omitted. Use `--help` for all options; `-h` selects Hexdump. Recording and export create new files.

<details>
<summary>📸 Screenshots &amp; terminal demo</summary>

<p align="center">
  <img src="assets/xserial-workbench.png" width="960" alt="Full-screen device console with a serial configuration sidebar">
  <img src="assets/xserial-inspector.png" width="960" alt="Binary workbench inspecting a Modbus request and response">
</p>

RawUI, the full-screen console, and Hexdump in 42 seconds:

https://github.com/user-attachments/assets/91dca830-0c6a-41ba-96f0-97ad97430697

</details>

## Documentation

- [MCP setup and workflows](docs/mcp.md)
- [Binary workbench](docs/workbench.md)
- [Recording, replay, and export](docs/capture.md)
- [Modbus examples](examples/modbus/README.md)

## Contributing

[Issues](https://github.com/ZhiWei-Ou/xserial/issues) and pull requests are welcome. Keep both READMEs in sync.

## License

[MIT](LICENSE).
