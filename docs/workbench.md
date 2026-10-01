# Binary Serial Workbench

The workbench is for sending serial commands and debugging binary protocols. Default RawUI and `--TUI` serve device consoles. Use `--workbench` to edit binary commands; input is sent only when you press Enter.

## Try It Without Hardware

```bash
xserial demo
xserial demo --frame modbus-read
xserial demo --snapshot --record demo.xsr
```

The demo uses the real session core with a simulated serial port. The default request, `01 03 00 00 00 02 C4 0B`, reads two registers with values 100 and 101. `--snapshot` runs one query and prints the actual workbench view, suitable for non-interactive terminals and documentation previews.

The device address selects the scenario: address 1 returns a normal response, address 2 deliberately corrupts the response CRC, address 3 splits the response across two reads, and address 4 joins two responses into one read. After changing the address, remove the old CRC and use Ctrl-K to append a new CRC16 Modbus checksum. Load `examples/modbus/commands.json` to try all four scenarios.

## Hex Editing and Command Favorites

```bash
xserial /dev/ttyUSB0 115200 --workbench
xserial COM3 --workbench --commands my-commands.json
```

The editor accepts compact Hex with an even number of digits, whitespace or comma separators, and a `0x` prefix for each byte. Pasting multiple lines does not send them. The input area previews normalized bytes, and validation errors identify the character position. No CR, LF, or NUL is appended implicitly.

| Key | Action |
| --- | --- |
| Enter | Validate the entire input and send |
| Left / Right, Home, Backspace, Delete | Edit input |
| Ctrl-U | Clear input |
| Up / Down | Browse successful sends and return to the current draft |
| Page Up / Page Down | Browse traffic; scrolling away from the bottom pauses automatic following |
| End | Resume following the latest traffic |
| Ctrl-S | Name and save the current command; an existing name updates that command |
| Ctrl-O | Load a favorite into the editor for review |
| Ctrl-P | Open the local command menu |
| Ctrl-C | Quit and restore the terminal |

Favorites default to `xserial/commands.json` under the system's user configuration directory. The file is created only when you save a command. A corrupt existing file causes an error and is not automatically replaced. Failed sends retain the input; commands are not queued while disconnected.

## Byte Selection and Checksums

Tab opens the traffic inspector. Up / Down selects an entry, Left / Right changes the byte offset, `[` and `]` adjust the width, and Enter copies the selected bytes into the editor. Selecting exactly 1, 2, 4, or 8 bytes shows integer interpretations. Selections of 4 or 8 bytes also show floating-point values. Little-endian and big-endian results are listed separately; incomplete fields are never padded.

Ctrl-K opens the checksum helper. The input must contain the payload without an existing checksum. Choose an algorithm, preview the final bytes, then press Enter to append the checksum and return to the editor. Appending a checksum does not send the command.

| Algorithm | Parameters and Output |
| --- | --- |
| CRC16 Modbus | Polynomial 0x8005, reflected implementation 0xA001, initial value 0xFFFF, final XOR 0; low byte first |
| SUM8 | Sum of payload bytes modulo 256; one output byte |
| XOR8 | Bitwise XOR of payload bytes; one output byte |

The inspector also checks the trailing checksum of the entire entry. Without framing, the result applies only to the current data block. A failed checksum on a partial read does not mean that the complete protocol frame is corrupt.

## Receive Framing

```bash
xserial /dev/ttyUSB0 --workbench --frame fixed:9
xserial /dev/ttyUSB0 --workbench --frame delimiter:0D0A
xserial /dev/ttyUSB0 --workbench --frame length:2:1:5:be
xserial /dev/ttyUSB0 --workbench --frame modbus-read
```

| Rule | Meaning |
| --- | --- |
| `chunk` | Default; each read is displayed as a data block |
| `fixed:N` | Every N bytes form one frame |
| `delimiter:HEX` | The specified delimiter ends a frame and remains part of it |
| `length:OFFSET:WIDTH:OVERHEAD:le` or `be` | Read a 1-, 2-, or 4-byte length at the zero-based offset. Total frame length is that value plus OVERHEAD, which includes the header and trailer |
| `modbus-read` | Use the byte count of a function 03/04 response, or the fixed length of an 83/84 exception response |

The framer handles split frames and multiple frames joined in one read. Until a complete frame arrives, the input preview shows the number of buffered bytes. Individual frames and incomplete buffers are limited to 65536 bytes. Invalid lengths produce an error and clear the pending buffer. Disconnecting or clearing traffic also resets it. Recordings retain the original bytes before framing.

`modbus-read` is a structural helper for register-read responses. It is not a complete Modbus RTU implementation and does not validate inter-character or silent intervals on the bus. Transmission timing and device behavior still need to satisfy the actual protocol requirements.

## Modbus Query Walkthrough

1. Run `xserial demo --frame modbus-read` and press Enter to send the default query.
2. The response is `01 03 04 00 64 00 65 7B C7`. Press Tab, select offset 3 and width 2: big-endian uint16 shows 100, and CRC16 Modbus for the entire frame shows OK.
3. Press Esc to close the inspector. Change the request to `02 03 00 00 00 02`, use Ctrl-K to append the CRC, and send it. Inspect the deliberately corrupted response checksum.
4. Change the request to `03 03 00 00 00 02` and append a new CRC. The same framing rule combines the two reads into one frame.
5. Use `--record` to save the session, then inspect it offline with `xserial replay`.

For CRC and protocol fields, see the [Modbus Serial Line Guide V1.02](https://www.modbus.org/file/secure/modbusoverserial.pdf). This example covers manual sending, field interpretation, and checksum verification.

## Search and Problem Marks

Press Ctrl-F, enter a Hex pattern, and press Enter to search retained traffic. The inspector selects the first matching entry; N moves to the next match. Searches operate on data blocks or assembled frames. Configure appropriate framing to search across read boundaries.

Ctrl-B adds a note to the inspected entry, or to the latest entry when the inspector is closed. Live sessions need `--record` to save marks. Offline playback saves marks in a `.marks.json` sidecar beside the source recording, leaving the source unchanged. Exports include saved marks.

## History and Resource Limits

Displayed traffic retains at most 2000 complete entries and 1 MiB of bytes. When either limit is exceeded, the oldest complete entries are removed; RX/TX statistics continue accumulating. Sending history retains 100 commands, input is limited to 16384 characters, and favorites hold at most 100 commands. Device bytes are displayed as Hex and safe ASCII; ANSI control sequences are never executed.

Recording writes synchronously and applies backpressure. File errors end the session. Playback loads at most 128 MiB of bytes and annotation content, and 1000000 events; larger recordings need to be split into smaller sessions. See the [recording guide](capture.md) for the format and export examples.
