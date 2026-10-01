# Session Recording Format

`--record` saves original serial traffic and session events as versioned JSONL. The recommended extension is `.xsr`. Recording happens at the serial adapter boundary, before display transformations and transfer handling. Terminal timestamps, Hex rendering, and framing do not change the recorded bytes.

```bash
xserial /dev/ttyUSB0 --record session.xsr
xserial demo --snapshot --record demo.xsr
xserial replay demo.xsr --frame modbus-read
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

## Replay and Marks

Replay only displays recorded traffic locally. It never opens a serial port or sends historical commands to a device. Space pauses playback, `+` and `-` adjust speed between 0.25x and 16x, and R restarts. The interface stays open for search and inspection when playback ends. Pausing or changing speed restarts the current event interval using the new playback state.

Ctrl-B marks selected traffic. Live marks are added to the recording; replay marks are saved in `<capture>.marks.json`, with `at` and `note` for each mark. The source recording is unchanged. Corrupt existing mark files produce an error.

## Export Excerpts

```bash
xserial export session.xsr --from 2s --to 10s
xserial export session.xsr --match "00 64" -o matches.txt
xserial export session.xsr --from 2s --to 10s --format jsonl -o excerpt.xsr
xserial replay excerpt.xsr
```

Time ranges are relative to the recording start and include both endpoints; `to=0` means no upper bound. Hex matching is limited to bytes within one event. Text output escapes notes and errors. JSONL export preserves original `seq` and `request` values: filtering can leave sequence gaps and result events referencing requests outside the excerpt. Sidecar marks are appended as `mark` events in the export.

Export writes to stdout by default. Specifying an output file uses exclusive creation. The parser rejects corrupt, truncated, incompatible-version, or invalid-sequence files. Each line is limited to 128 KiB; replay is limited to 128 MiB of bytes and annotation content, and 1000000 events.
