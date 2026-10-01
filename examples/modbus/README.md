# Modbus 调试示例

本目录提供正常查询、坏响应 CRC 和分段响应的命令收藏，以及真实模拟查询生成的双向录制。

```bash
xserial demo --frame modbus-read --commands examples/modbus/commands.json
xserial replay examples/modbus/demo.xsr --frame modbus-read
xserial export examples/modbus/demo.xsr
```

Ctrl-O 选择收藏并加载，再按 Enter 发送。设备地址 1 返回正常数据，地址 2 故意破坏响应 CRC，地址 3 分两次读取返回，地址 4 将两个响应合并返回。坏 CRC 场景的请求本身仍带正确校验，用于检查设备响应。

正常响应字段为 `01 03 04 00 64 00 65 7B C7`，两个大端寄存器分别为 100 与 101。详细操作见 [工作台说明](../../docs/workbench.md)。
