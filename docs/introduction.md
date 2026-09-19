# xserial 项目介绍

[English README](../README.md)

xserial 是一个专注于串口通信的跨平台终端工具。它不试图成为串口库或通用协议框架，而是把常见的设备调试流程做得直接可靠：找到端口、建立连接，然后开始与设备交互。

它适合以下场景：

- 进入开发板、Linux 设备或 MCU 的 Shell/Console；
- 观察设备输出并直接发送键盘输入；
- 保存接收数据，或为每行增加时间戳；
- 发送原始文件，或通过 YMODEM 传输单个文件；
- 在全屏界面中查看连接状态、流量统计并调整串口配置。

## 安装

```bash
go install github.com/ZhiWei-Ou/xserial/cmd/xserial@latest
```

安装后可以查看版本，确认 Go 的 bin 目录已经加入 `PATH`：

```bash
xserial version
```

## 快速开始

列出系统中的串口：

```bash
xserial list
```

使用默认的 `115200,8,N,1` 配置连接设备：

```bash
xserial /dev/ttyUSB0
```

macOS 上的端口通常类似 `/dev/cu.usbserial-0001`，Windows 可以直接使用 `COM3` 这样的端口名。

连接命令统一使用以下形式：

```text
xserial <port> [cfg]
```

`cfg` 按 `baud,data-bits,parity,stop-bits` 排列，允许从右侧省略未指定的字段：

```bash
xserial /dev/ttyUSB0 9600
xserial /dev/ttyUSB0 9600,7
xserial /dev/ttyUSB0 9600,7,E
xserial /dev/ttyUSB0 9600,7,E,2
```

奇偶校验支持 `N`、`O`、`E`、`M`、`S`，停止位支持 `1`、`1.5`、`2`。不能用空字段跳过中间配置。

## RawUI：透明串口终端

RawUI 是默认界面，适合文本输出、Shell 和 Console 设备。普通键盘字节直接发送给设备，设备返回的字节直接写入 `stdout`。xserial 自身的提示、进度和错误写入 `stderr`，因此默认不会污染设备输出。

启动和后台重连保持静默，按 `Ctrl-P i` 可查看端口、波特率、数据位、校验位和停止位。

本地命令以 `Ctrl-P` 为前缀：

| 按键 | 作用 |
|---|---|
| `Ctrl-P i` | 显示连接配置 |
| `Ctrl-P h` | 显示本地帮助 |
| `Ctrl-P u` | 发送原始文件 |
| `Ctrl-P Ctrl-U` | 使用 YMODEM 上传文件 |
| `Ctrl-P Ctrl-D` | 使用 YMODEM 下载文件 |
| `Ctrl-P q` | 退出连接 |
| `Ctrl-P Ctrl-P` | 向设备发送字节 `0x10` |

原始文件发送只负责字节透传，不提供校验、重传或断点续传。需要可靠的单文件传输时，可以使用 YMODEM。路径输入或传输过程中按 `Esc` 可以取消并返回普通串口交互。

## TUI：全屏终端工作区

加入 `--TUI` 可以进入全屏界面（Beta，不稳定）：

```bash
xserial /dev/ttyUSB0 --TUI
```

TUI 在终端交互之外集中显示连接状态和收发统计，并提供串口配置侧栏。配置端口、波特率或帧参数后，确认操作会使用新配置替换当前连接。

常用操作：

| 按键 | 作用 |
|---|---|
| `Ctrl-P` | 打开或关闭命令面板 |
| `Ctrl-P c` | 将焦点切换到配置侧栏 |
| `Ctrl-C` | 退出连接 |
| `Esc` | 关闭弹窗或取消当前操作 |

命令面板可以发送原始文件、清空终端内容、取消传输或退出。配置列表支持方向键和 Vim 风格的 `h/j/k/l` 导航。

## 日志与时间戳

使用 `--log` 将接收数据追加到文件：

```bash
xserial /dev/ttyUSB0 --log device.log
```

使用 `-t` 或 `--time` 开启时间戳，格式固定为 `15:04:05.000`（时:分:秒.毫秒），不支持自定义格式：

```bash
xserial /dev/ttyUSB0 --time
xserial /dev/ttyUSB0 --log device.log -t
```

不启用 `--time` 时，RawUI 的设备输出保持逐字节透明。启用后，实时输出和接收日志会按行增加时间前缀。

## 断线重连

连接断开后，xserial 在后台每隔 1 秒重试，直到连接恢复或用户退出，不限制次数。不再提供 `--reconnect` 参数，断线、重试和恢复连接均保持静默。首次连接失败仍直接返回错误。

重连期间不会缓存键盘输入，也不会在连接恢复后补发。正在进行的文件传输会失败，不会自动续传。

## 设计原则

xserial 始终把设备数据通道和本地界面分开：

```text
stdin  -> xserial -> serial device
stdout <- xserial <- serial device
stderr <- local help, status, progress, and errors
```

默认 RawUI 优先保证数据透明和终端状态恢复；TUI 则在同一串口会话能力之上提供全屏交互。串口的打开、重连和关闭只有一个明确所有者，文件传输期间也会独占所需的数据方向，避免用户输入与文件内容交错。

## 更多文档

- [架构说明](architecture.md)
- [XFER 协议](xfer.md)
