# 二进制串口调试工作台

工作台面向串口指令与二进制协议联调。默认 RawUI 和 `--TUI` 继续服务文本终端；二进制编辑通过 `--workbench` 显式进入，输入只有按 Enter 才会发送。

## 无需硬件的体验

```bash
xserial demo
xserial demo --frame modbus-read
xserial demo --snapshot --record demo.xsr
```

Demo 使用真实会话内核和模拟串口。默认请求 `01 03 00 00 00 02 C4 0B` 查询两个寄存器，返回数值 100 和 101。`--snapshot` 完成一次查询并输出真实工作台视图，适合非交互终端与文档生成。

模拟场景按设备地址区分：地址 1 返回正常响应；地址 2 故意破坏响应 CRC；地址 3 将响应分成两次读取；地址 4 将两个响应合并为一次读取。更改地址后应先删除旧 CRC，再通过 Ctrl-K 重新追加 CRC16 Modbus。可直接加载 `examples/modbus/commands.json` 体验四种场景。

## Hex 编辑与命令收藏

```bash
xserial /dev/ttyUSB0 115200 --workbench
xserial COM3 --workbench --commands my-commands.json
```

Hex 编辑器支持连续偶数位 Hex、空白或逗号分隔，以及逐字节 `0x` 前缀。粘贴多行内容不会直接发送。输入区显示规范化后的字节预览，错误包含字符位置；不隐含追加 CR、LF 或 NUL。

| 按键 | 操作 |
| --- | --- |
| Enter | 完整校验后发送 |
| 左右箭头、Home、Backspace、Delete | 编辑输入 |
| Ctrl-U | 清空输入 |
| 上下箭头 | 浏览成功发送的历史，返回当前草稿 |
| PageUp 和 PageDown | 浏览流量，离开底部后暂停自动跟随 |
| End | 恢复跟随最新流量 |
| Ctrl-S | 为当前命令命名并保存；同名命令更新 |
| Ctrl-O | 加载收藏，确认后放回编辑器 |
| Ctrl-P | 本地命令菜单 |
| Ctrl-C | 退出并恢复终端 |

收藏默认位于系统用户配置目录下的 `xserial/commands.json`，只有保存时才创建。损坏的现有文件会报错，不会被自动替换。发送失败保留输入；断线期间不缓存待发送命令。

## 字段选择与校验

Tab 打开流量检查面板。上下箭头切换条目，左右箭头选择字节偏移，`[` 和 `]` 调整宽度，Enter 将选中字节复制回输入区。选择恰好 1、2、4、8 字节时展示对应整数；4、8 字节同时展示浮点数，分别列出大小端结果，不补齐不足的字段。

Ctrl-K 打开校验助手。输入必须是尚未包含校验的载荷；选择算法后先预览完整字节，再按 Enter 追加，返回编辑器检查。追加动作本身不会发送。

| 算法 | 参数与输出 |
| --- | --- |
| CRC16 Modbus | 多项式 0x8005，反射实现 0xA001，初值 0xFFFF，最终异或 0；低字节先发送 |
| SUM8 | 所有载荷字节之和模 256，输出一个字节 |
| XOR8 | 所有载荷字节逐位异或，输出一个字节 |

检查面板同时验证整个条目的尾部校验。未配置分帧时，结果只对应当前数据块；不能把一个分段读取的块校验失败当作完整协议帧损坏。

## 接收分帧

```bash
xserial /dev/ttyUSB0 --workbench --frame fixed:9
xserial /dev/ttyUSB0 --workbench --frame delimiter:0D0A
xserial /dev/ttyUSB0 --workbench --frame length:2:1:5:be
xserial /dev/ttyUSB0 --workbench --frame modbus-read
```

| 规则 | 含义 |
| --- | --- |
| chunk | 默认，每次读取只标为数据块 |
| fixed:N | 每 N 个字节组成一帧 |
| delimiter:HEX | 指定分隔符结束一帧，保留分隔符 |
| length:OFFSET:WIDTH:OVERHEAD:le 或 be | 零起始偏移处读取 1、2、4 字节长度；总帧长为长度值加 OVERHEAD，OVERHEAD 包括头部与尾部 |
| modbus-read | 按功能码 03/04 的响应字节数，或 83/84 异常响应长度组帧 |

分帧器处理半帧和多帧粘连，完整数据到齐前显示待接收字节数。单帧和未完成缓冲上限为 65536 字节；无效长度会报错并清空待组帧数据。断线和清屏也重置该缓冲。原始录制仍保存分帧前的数据。

`modbus-read` 是寄存器读取响应的结构辅助，不能代替全部 Modbus RTU 协议实现，也不验证总线上的字符间隔或静默间隔。发送时序和设备行为仍应按真实协议要求确认。

## Modbus 查询演示

1. 运行 `xserial demo --frame modbus-read`，按 Enter 发送默认查询。
2. 响应为 `01 03 04 00 64 00 65 7B C7`。Tab 打开检查面板，移动到偏移 3，宽度设为 2；大端 uint16 显示 100，整帧 CRC16 Modbus 显示 OK。
3. Esc 关闭面板，将请求改为 `02 03 00 00 00 02`，Ctrl-K 追加 CRC 后发送，观察故意破坏校验的响应。
4. 改为 `03 03 00 00 00 02` 并重新追加 CRC；相同分帧规则会把两次读取合并为一帧。
5. 使用 `--record` 保存过程；之后通过 `xserial replay` 离线查看。

CRC 与协议字段参考 [Modbus Serial Line Guide V1.02](https://www.modbus.org/file/secure/modbusoverserial.pdf)。本项目的示例范围为手工发送、字段解释和校验。

## 搜索与问题标记

Ctrl-F 输入 Hex 模式，Enter 搜索当前保留的流量。检查面板定位第一个匹配条目，N 跳到下一个。搜索按数据块或已组好的帧匹配；需要跨读取边界搜索时先配置适当分帧。

Ctrl-B 为当前检查条目添加说明；不在检查面板时选择最新条目。现场会话需要启用 `--record` 才能保存标记。离线回放将标记写入源记录旁的 `.marks.json`，保留源文件，导出时带上标记。

## 历史与性能边界

显示历史最多保留 2000 个完整条目和 1 MiB 字节，超限从最旧的完整条目回收；收发统计继续累计。发送历史保留 100 条，输入上限为 16384 字符，命令收藏最多 100 条。设备字节只作为 Hex 和安全 ASCII 显示，不执行 ANSI 控制序列。

录制同步写入并施加背压，文件错误结束会话。回放最多读取 128 MiB 的字节及说明内容、1000000 个事件；更大的录制需要先按场景拆分。具体记录格式和导出示例见 [录制说明](capture.md)。
