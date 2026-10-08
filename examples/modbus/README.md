# Modbus Debugging Examples

`demo.xsr` contains a simulated Modbus query and response for offline playback and export. Run these commands from the repository root:

```bash
xserial replay examples/modbus/demo.xsr --hexdump --frame modbus-read
xserial export examples/modbus/demo.xsr
```

The recorded response is `01 03 04 00 64 00 65 7B C7`. The two big-endian registers contain 100 and 101.

`commands.json` provides a favorite for reading two holding registers starting at address 0 on device address 1. Use it with a device that supports that query, adjusting the port and serial settings:

```bash
xserial /dev/ttyUSB0 9600 --workbench --frame modbus-read --commands examples/modbus/commands.json
```

Press Ctrl-O to select and load the favorite, then review it and press Enter to send. See the [workbench guide](../../docs/workbench.md) for editing, checksums, and field inspection.
