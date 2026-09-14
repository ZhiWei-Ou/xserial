<p align="center">
  <img src="assets/xserial-logo.svg" width="160" alt="xserial project logo">
</p>

<h1 align="center">xserial</h1>

<p align="center">
  A focused, cross-platform serial terminal for device consoles, raw byte streams, and file transfer.
</p>

xserial keeps the common path short: list a port, connect to it, and start typing. Its default RawUI behaves like a traditional transparent serial terminal, while the optional full-screen TUI adds an integrated terminal workspace and live serial configuration.

## Features

- Direct connection syntax: `xserial <port> [cfg]`
- Serial port discovery on Linux, macOS, and Windows
- Byte-transparent RawUI with local commands on `stderr`
- Full-screen TUI with terminal rendering, connection status, statistics, and editable serial settings
- Raw file upload and single-file YMODEM transfer
- Receive logging, optional line timestamps, and automatic background reconnection
- Shell completion for Bash, Zsh, Fish, and PowerShell

## Install

```bash
go install github.com/ZhiWei-Ou/xserial/cmd/xserial@latest
```

Make sure the Go bin directory is available in your `PATH`, then verify the installation:

```bash
xserial version
```

## Quick start

List available serial ports:

```bash
xserial list
```

Connect with the default `115200,8,N,1` configuration:

```bash
xserial /dev/ttyUSB0
```

Use a different baud rate or frame configuration:

```bash
xserial /dev/ttyUSB0 9600
xserial /dev/ttyUSB0 9600,7,E,2
```

The optional configuration follows:

```text
baud[,data-bits[,parity[,stop-bits]]]
```

Open the full-screen interface:

```bash
xserial /dev/ttyUSB0 --tui
```

On macOS, ports commonly look like `/dev/cu.usbserial-0001`. On Windows, use the COM name directly, for example `xserial COM3`.

## Interfaces

RawUI is the default. Keyboard input is sent directly to the device, and received bytes are written to `stdout` without local UI output mixed into the stream. Press `Ctrl-P h` for local commands or `Ctrl-P q` to leave the session.

The full-screen TUI provides a terminal workspace, connection statistics, a command palette, and an editable configuration sidebar. Use `Ctrl-P` for commands, `Ctrl-P c` to focus serial configuration, and `Ctrl-C` to quit.

## Hexdump output

Display received bytes as offset, hexadecimal bytes, and ASCII (similar to `hexdump -C`):

```bash
xserial /dev/ttyUSB0 -h
xserial /dev/ttyUSB0 --hexdump --time
```

Each received chunk is printed immediately in rows of up to 16 bytes, with continuous byte offsets and CRLF line endings. `--time` prefixes each dump row. Keyboard input and receive logging keep their existing behavior. Hexdump is available in RawUI and cannot be combined with `--tui`. Use `--help` for CLI help; `-h` selects hexdump.

## Logging and timestamps

Append received data to a file:

```bash
xserial /dev/ttyUSB0 --log device.log
```

Add the default millisecond timestamp or provide a custom Go time format:

```bash
xserial /dev/ttyUSB0 --time
xserial /dev/ttyUSB0 --log device.log --time="2006-01-02 15:04:05.000"
```

## Documentation

- [Chinese introduction](docs/introduction.md)
- [Architecture](docs/architecture.md)
- [XFER protocol](docs/xfer.md)

## Development

```bash
go test ./...
```
