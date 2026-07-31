# Workspace Context

- Mode: work
- Commit: standard

## Product

- xserial is a CLI tool focused on serial communication.
- rawui is intended for devices whose peer-side interaction is text-based or shell-like.
- tui is intended for binary data transmission and reception.
- RawUI logs use `time [LEVEL] message`; only the level text is colored, and
  connection parameters are emitted as separate log entries.
- Application logs use `internal/logging`; leveled calls go through its
  formatter and filter, while raw local UI output bypasses both.
- The CLI lists ports with no positional arguments and connects with `xserial <port> [baud]`; it has no `conn` or `list` subcommands.
