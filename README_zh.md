<p align="center">
  <img src="assets/xserial-logo.png" width="160" alt="xserial 标志">
</p>

<h1 align="center">xserial</h1>

<p align="center">
  终端里的串口通信与二进制协议调试工作台。
</p>

<p align="center">
  <!-- 创建新版本标签时同步更新 assets/version-badge.svg 及其替代文本。 -->
  <a href="https://github.com/ZhiWei-Ou/xserial/tags"><img src="assets/version-badge.svg" alt="版本 v0.1.0"></a>
  <a href="https://github.com/ZhiWei-Ou/xserial/actions/workflows/ci.yml"><img src="https://github.com/ZhiWei-Ou/xserial/actions/workflows/ci.yml/badge.svg" alt="构建状态"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT 许可证"></a>
</p>

<p align="center">
  <a href="#文档">文档</a> ·
  <a href="#快速开始">体验 Demo</a> ·
  <a href="https://github.com/ZhiWei-Ou/xserial/issues">问题反馈</a>
</p>

<p align="center">
  <a href="README.md">English</a> · 简体中文
</p>

<p align="center">
  <strong>演示（42 秒）</strong>：RawUI、全屏控制台与 Hexdump。
</p>

https://github.com/user-attachments/assets/91dca830-0c6a-41ba-96f0-97ad97430697

## 核心功能

- **让 coding agent 调试设备。** MCP 接入正在运行的串口终端，提供精确字节发送和带游标的有限等待读取；本地唯一 HTTP daemon 发现会话，终端连接与历史在 MCP 重启后仍然保留。

- **发送与观察字节。** 编辑经过校验的 Hex 输入，对照带时间、长度、Hex 和 ASCII 的 TX/RX，复用发送历史与命令收藏。
- **理解二进制字段。** 选中字节，查看大小端整数与浮点数；预览、追加和检查 CRC16 Modbus、SUM8、XOR8 校验。
- **重组响应。** 显式选择固定长度、分隔符、长度字段或 Modbus 寄存器响应分帧，处理半帧与粘连。
- **离线复查通信。** 保存原始 RX/TX 与连接事件，设备断开后继续回放、搜索、标记和导出。
- **无需硬件即可体验。** 内置模拟设备通过同一个工作台演示正常响应、坏 CRC、分段响应和粘连响应。
- **保留常用串口终端能力。** 提供字节透明的 RawUI 与全屏终端 UI，支持自动重连、接收日志、时间戳和单文件 YMODEM 传输，覆盖 Linux、macOS、Windows。

## 快速开始

使用 Go 1.26.2 或更新版本，从源码运行无需硬件的 Demo：

```bash
git clone https://github.com/ZhiWei-Ou/xserial.git
cd xserial
go run ./cmd/xserial demo --frame modbus-read
```

按 **Enter** 查询两个模拟寄存器，按 **Tab** 检查响应，按 **Ctrl-C** 退出。无需交互终端的预览可使用 `go run ./cmd/xserial demo --snapshot`。

工作台改动尚未公开发布，请使用当前源码体验。发布后可从 [Releases 页面](https://github.com/ZhiWei-Ou/xserial/releases) 下载预编译包。

运行 `make build` 可将本地可执行文件构建到 `bin/`。后续示例假设 `xserial` 已加入 `PATH`；在仓库根目录中，也可以将 `xserial` 替换为 `go run ./cmd/xserial`。

## 使用方法

### 连接设备控制台

```bash
xserial list
xserial /dev/ttyUSB0
xserial /dev/ttyUSB0 9600,7,E,2
```

默认 RawUI 将键盘输入直接发送给设备，接收字节原样写入 `stdout`，本地提示写入 `stderr`。按 `Ctrl-P h` 查看本地命令，按 `Ctrl-P q` 退出。

macOS 可使用 `/dev/cu.usbserial-0001` 这样的设备路径；Windows 使用 `COM3` 这样的端口名。

打开带串口配置侧栏的全屏设备控制台：

```bash
xserial /dev/ttyUSB0 --TUI
```

此终端 UI 处于 Beta。按 `Ctrl-P` 打开命令菜单，按 `Ctrl-P c` 聚焦配置侧栏，按 `Ctrl-C` 退出。

<p align="center">
  <img src="assets/xserial-workbench.png" width="960" alt="xserial 全屏设备控制台与串口配置侧栏">
</p>

### 调试二进制协议

```bash
xserial /dev/ttyUSB0 --workbench
xserial demo --frame modbus-read --commands examples/modbus/commands.json
```

支持粘贴 `AA 01`、`AA01`、`AA,01` 或 `0xAA 0x01`，只有按 Enter 才会发送。Demo 收藏提供四种响应场景。

| 按键 | 操作 |
| --- | --- |
| Enter | 发送当前 Hex 命令 |
| 上 / 下 | 浏览成功发送的历史 |
| Ctrl-S / Ctrl-O | 保存 / 加载命令收藏 |
| Tab | 检查流量字节 |
| 左 / 右、`[` / `]` | 在检查面板中选择字节偏移与宽度 |
| Ctrl-K | 预览并追加校验和 |
| Ctrl-F / Ctrl-B | 搜索 Hex 字节 / 标记选中流量 |
| Page Up / Page Down、End | 浏览流量 / 跟随最新数据 |
| Ctrl-P / Ctrl-C | 打开命令菜单 / 退出 |

未显式选择分帧规则时，RX 只作为**数据块**展示。设备控制字节作为数据显示，不会在工作台中执行。`modbus-read` 根据响应结构组帧，不实现 RTU 静默时间或完整 Modbus 主站。

<p align="center">
  <img src="assets/xserial-inspector.png" width="960" alt="xserial 二进制工作台展示带时间戳与 ASCII 的 Modbus Hex 请求和响应">
</p>

### 录制、回放与导出

```bash
xserial /dev/ttyUSB0 --workbench --record session.xsr
xserial replay session.xsr --frame modbus-read
xserial export session.xsr --match "00 64"
xserial export session.xsr --from 2s --to 10s --format jsonl -o excerpt.xsr
```

回放在本地离线进行，不打开串口。Space 暂停，`+` / `-` 调整倍速，R 重新播放。播放结束后仍可搜索和检查流量。

现场标记写入录制文件，回放标记保存在 `.marks.json` 侧边文件中，导出包含已保存的侧边标记。录制与导出创建新文件，不覆盖已有文件。

### 查看 Hexdump 与接收日志

```bash
xserial /dev/ttyUSB0 --hexdump --time
xserial /dev/ttyUSB0 --log device.log --time
```

Hexdump 在 RawUI 中展示偏移、Hex 和 ASCII。接收日志将设备输出追加到文件；`--time` 为接收行或 Hexdump 行添加 `HH:MM:SS.mmm` 时间戳。

## 常用配置

连接语法为 `xserial <port> [cfg]`，右侧省略的字段使用默认值 `115200,8,N,1`：

```text
baud[,data-bits[,parity[,stop-bits]]]
```

```bash
xserial /dev/ttyUSB0 115200
xserial /dev/ttyUSB0 9600,8,E,1 --workbench --frame fixed:9
```

校验位支持 `N`、`O`、`E`、`M`、`S`；停止位支持 `1`、`1.5`、`2`。

| 选项 | 用途 |
| --- | --- |
| `--workbench` | 二进制协议调试工作台 |
| `--TUI` | 全屏设备控制台，处于 Beta |
| `--commands <file>` | 工作台命令收藏，默认位于用户配置目录的 `xserial/commands.json` |
| `--frame <rule>` | 工作台 RX 分帧规则，默认为 `chunk` |
| `--record <file>` | 原始 RX/TX 与连接事件录制 |
| `--hexdump`、`-h` | RawUI 的 Hex 与 ASCII 输出 |
| `--log <file>` | 将接收数据追加到文件 |
| `--time`、`-t` | RawUI 与终端 UI 的接收时间戳 |

分帧规则包括 `chunk`、`fixed:N`、`delimiter:HEX`、`length:OFFSET:WIDTH:OVERHEAD:le|be` 和 `modbus-read`，确切语义见[工作台说明](docs/workbench.md)。

`--workbench`、`--TUI`、`--hexdump` 互斥。使用 `--help` 查看命令行帮助，`-h` 表示 Hexdump。`xserial version` 输出版本号，`xserial --version` 同时展示可用的构建信息。

## 文档

- [English README](README.md)
- [工作台说明与 Modbus 示例](docs/workbench.md)
- [录制格式、回放与导出](docs/capture.md)
- [MCP 设备调试与 daemon 生命周期](docs/mcp.md)
- [协议示例与录制样本](examples/modbus/README.md)

以上详细指南使用英文。

## 参与贡献

欢迎问题反馈、文档改进和 Pull Request。[提交 Issue](https://github.com/ZhiWei-Ou/xserial/issues) 时，请提供操作系统、xserial 版本、串口配置、复现步骤，以及预期和实际行为。较大改动建议先通过 Issue 说明方案。

修改代码前，请阅读 [AGENTS.md](AGENTS.md)。保持改动聚焦，保留 RawUI 的字节透明契约，为行为变更添加回归测试，并同步维护两种语言的 README。

```bash
go test ./...
go test -race ./...
make all
```

每次改动运行单元测试，并发代码改动运行 race 测试，平台代码改动运行相关交叉构建。`make all` 构建 Linux、macOS、Windows 的 amd64 与 arm64 二进制。

## 许可证

采用 [MIT 许可证](LICENSE)。
