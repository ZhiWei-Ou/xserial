# Workspace Context

- Mode: work
- Commit: standard

## Product

- xserial is a CLI tool focused on serial communication.
- rawui is the byte-transparent classic serial session.
- tui is a full-screen interactive serial terminal for Linux boards, with a
  configuration sidebar and terminal-like immediate keyboard input. It does
  not need to provide complete VT emulation; preserve ANSI SGR colors and
  interpret common VT cursor, erase, insert, delete, and scrolling controls.
  Its full-screen UI uses a pure black background and Claude-style orange
  borders, including overlays. The sidebar contains serial configuration;
  connection state and session statistics belong in the bottom status bar.
- RawUI logs use `time [LEVEL] message`; only the level text is colored, and
  connection parameters are emitted as separate log entries.
- Application logs use `internal/logging`; leveled calls go through its
  formatter and filter, while raw local UI output bypasses both.
- The CLI shows help with no positional arguments, lists ports with `xserial list`, reports its version only through `xserial version`, and connects with `xserial <port> [cfg]`; positional cfg is `baud[,data-bits[,parity[,stop-bits]]]` with defaults `115200,8,N,1`.
