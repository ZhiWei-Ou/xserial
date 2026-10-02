# Session Recording Format

`--record` saves original serial traffic and session events as versioned JSONL. The recommended extension is `.xsr`. Recording happens at the serial adapter boundary, before display transformations and transfer handling. Terminal timestamps, Hex rendering, and framing do not change the recorded bytes.

```bash
xserial /dev/ttyUSB0 --record session.xsr
xserial demo --snapshot --record demo.xsr
xserial replay session.xsr
xserial replay demo.xsr --hexdump --frame modbus-read
```

The first line contains `format=xserial-capture`, `version=1`, the start time, and serial configuration. Each subsequent line contains an event with a strictly increasing `seq`, observation time `at`, `kind`, and optional `data`, `request`, `error`, and `note` fields. Byte data is encoded as a Base64 JSON string.

| Kind | Meaning |
| --- | --- |
| `connected`, `disconnected`, `reconnecting`, `reconnected`, `closed` | Connection lifecycle |
| `tx_request` | Complete request submitted to the shared serial writer |
| `tx` | Bytes actually accepted by one Write call; partial writes are recorded too |
| `tx_complete` | Complete request acknowledged as sent; `request` refers to the `tx_request` sequence number |
| `tx_failed` | Request not acknowledged as fully sent; retains the error and request reference |
| `rx` | Original bytes returned by one Read call |
| `mark` | Problem note; `at` refers to the observation time of the marked traffic |

If an error occurs after a positive partial write, the accepted bytes remain recorded as `tx` and the request result is `tx_failed`. Cancellation can happen after the physical write but before acknowledgement, so examine both TX bytes and request results. Observation times do not promise hardware-level timing accuracy; `seq` defines the file order across concurrent tasks.

A mutex serializes record writes, applying synchronous disk backpressure without a background queue that could drop data. A recording write failure ends the session with an identifiable error. After the serial port and background tasks stop, the command layer closes the file and reports close errors. Recordings use exclusive file creation and never overwrite existing files.

## Replay

Replay only displays recorded traffic locally. It never opens a serial port or sends historical commands to a device. The default mode writes original RX bytes to the native terminal, preserving CR/LF, ANSI colors, cursor movement, and shell line redraws across reads. TX is not echoed again; the device's own echo remains in RX output.

Use `--hexdump` for a HEX/ASCII traffic view with timestamps and different TX/RX colors. It has no command editor or workbench menus. Both modes have just three controls:

| Key | Action |
| --- | --- |
| Space | Pause or resume, preserving the remaining event interval |
| r / R | Clear the display and replay from the beginning |
| Ctrl-C | Quit and restore the terminal |

Playback runs at the recorded speed. After completion, the display remains open for restart or quit; Space leaves completed playback unchanged. When input or output is redirected, playback runs once and exits. Redirected native output contains only original RX bytes.

### HEX Frame Boundaries

```bash
xserial replay session.xsr --hexdump
xserial replay session.xsr --hexdump --frame gap:50ms
xserial replay session.xsr --hexdump --frame delimiter:00
xserial replay session.xsr --hexdump --frame fixed:32
```

| Rule | Boundary |
| --- | --- |
| `newline` | Default: LF ends a frame; CRLF is one boundary, even across reads. A lone CR remains `0D` without ending the frame. |
| `gap:DURATION` | A gap strictly greater than the specified duration starts a new frame. Uses original observation timestamps, unaffected by pausing. |
| `delimiter:HEX` | The specified byte sequence ends a frame and remains visible; `delimiter:00` suits viewing COBS-encoded traffic. |
| `fixed:N` | Every N bytes form a frame, independently of read/write chunk boundaries. |

TX and RX maintain separate frame boundaries. Direction changes start a new display entry while preserving file order; they do not reset either stream's decoder. Long entries wrap to the terminal width without creating new frames. Bytes appear as they arrive, including incomplete tails at completion or disconnection. Disconnecting resets both decoders. `--frame` requires `--hexdump`.

Existing `chunk`, length-field, and `modbus-read` rules remain supported. `modbus-read` applies to RX responses; TX requests are displayed by write chunk. Byte-based decoders retain the 65536-byte frame limit. The HEX display retains at most 2000 entries and 1 MiB of recent bytes; RX/TX totals keep accumulating.

### Marks

Ctrl-B in the live workbench adds marks to a recording. Replay leaves the source recording unchanged and has no mark editor. Export includes recorded marks and existing `<capture>.marks.json` sidecars; corrupt sidecars produce an export error.

## Export Excerpts

```bash
xserial export session.xsr --from 2s --to 10s
xserial export session.xsr --match "00 64" -o matches.txt
xserial export session.xsr --from 2s --to 10s --format jsonl -o excerpt.xsr
xserial replay excerpt.xsr
```

Time ranges are relative to the recording start and include both endpoints; `to=0` means no upper bound. Hex matching is limited to bytes within one event. Text output escapes notes and errors. JSONL export preserves original `seq` and `request` values: filtering can leave sequence gaps and result events referencing requests outside the excerpt. Sidecar marks are appended as `mark` events in the export.

Export writes to stdout by default. Specifying an output file uses exclusive creation. The parser rejects corrupt, truncated, incompatible-version, or invalid-sequence files. Each line is limited to 128 KiB; replay is limited to 128 MiB of bytes and annotation content, and 1000000 events.
