# xserial

xserial 是一个简单、可靠的跨平台串口终端。

它既可以像 `screen` 一样通过 rawui 连接文本或 Shell 设备，也提供了一个面向二进制协议设备的全屏 TUI。你可以用它调试开发板、进入设备 Shell，或者以 Hex 形式收发二进制数据。

![xserial TUI](assets/screen.png)

## 为什么是 xserial？

串口调试本来应该是一件很直接的事：找到端口、连上设备，然后开始输入和观察输出。xserial 尽量保持这个过程简单，同时补上一些日常使用中很实用的能力：

- 紧凑的连接命令，默认使用常见的 `115200 / 8,N,1` 配置；
- raw 终端模式，设备字节不会被日志或界面信息污染；
- 面向文本与 Shell 设备的透明 rawui；
- 面向二进制数据的全屏 TUI，支持智能 Hex 输入、TX/RX 时间线和命令面板；
- 接收日志、行时间戳和 raw 文件上传；
- 支持 Linux、macOS 和 Windows。

## 安装

```bash
go install github.com/ZhiWei-Ou/xserial/cmd/xserial@latest
```

确保 Go 的 bin 目录已经加入 `PATH`。通常可以这样检查：

```bash
xserial version
```

## 快速开始

先查看系统中的串口：

```bash
xserial list
```

然后连接设备：

```bash
xserial /dev/ttyUSB0
xserial /dev/ttyUSB0 9600 --tui
```

除工具子命令外，位置参数都会被视为串口名；xserial 不猜测端口是否符合平台命名规则。

macOS 上的端口通常类似：

```bash
xserial /dev/cu.usbserial-0001
```

Windows 可以直接使用 COM 端口：

```powershell
xserial COM3
```

默认串口配置是 `115200,8,N,1`。需要其他波特率时，把它作为配置的第一项放在端口后面：

```bash
xserial /dev/ttyUSB0 9600
```

## Raw 模式

不带 `--tui` 时，xserial 运行在 raw 模式：键盘输入直接发送给设备，设备返回的原始字节直接写入终端。

```bash
xserial /dev/ttyUSB0 115200
```

本地命令使用 `Ctrl-P` 作为 prefix。先按 `Ctrl-P`，松开后再按命令键：

| 按键 | 作用 |
|---|---|
| `Ctrl-P h` | 显示本地帮助 |
| `Ctrl-P u` | 上传一个本地文件 |
| `Ctrl-P Ctrl-U` | 使用 YMODEM 上传一个本地文件 |
| `Ctrl-P Ctrl-D` | 使用 YMODEM 下载文件到当前目录 |
| `Ctrl-P q` | 退出连接 |
| `Ctrl-P Ctrl-P` | 向设备发送 `Ctrl-P` 字节（`0x10`） |

raw upload 是纯字节发送。它适合设备已经准备好接收固定长度数据的场景，但不提供校验、重传或断点续传。

使用 `--time` 时，rawui 会在每个接收行开头实时显示时间前缀；不使用该 flag 时仍保持设备输出字节透明。

YMODEM 上传会提示输入本地文件路径；YMODEM 下载使用发送端提供的文件名保存到当前目录。为避免意外覆盖，目标文件已存在时下载会失败。

YMODEM 完成信息包含文件大小和 CRC32；发生重试时会附加重试次数。协议帧被拒绝或校验失败并触发重传时，会同时输出 `transfer.ymodem_frame_retry` 警告日志。

输入文件路径或进行 raw/YMODEM 传输时，可以按 `Esc` 取消并返回普通串口透传。

## TUI 模式

> [!NOTE]
> TUI 目前处于 Beta 阶段，界面和交互方式仍可能调整，也可能存在尚未发现的显示问题。如果你更看重稳定性和字节透明传输，建议优先使用默认的 raw 模式。

加入 `--tui` 即可打开全屏界面：

```bash
xserial /dev/ttyUSB0 115200 --tui
```

TUI 会展示接收与发送字节数、统一的 TX/RX 流量时间线和操作状态。它只面向二进制数据；输入 Hex 字节并按 Enter 后，xserial 会原样发送解析得到的字节，不自动追加 `CR` 或换行。

常用操作：

| 按键 | 作用 |
|---|---|
| `Ctrl-P` | 打开或关闭命令面板 |
| `Ctrl-C` | 退出连接 |
| `↑` / `↓` | 浏览已发送的 Hex 历史 |
| `PageUp` / `PageDown` | 按页浏览流量历史 |
| `Esc` | 关闭命令面板、取消路径输入或取消传输 |

命令面板中可以：

- 上传 raw 文件；
- 清空当前显示历史；
- 取消正在进行的传输；
- 退出连接。

YMODEM 仍可在 rawui 中使用，但不属于 TUI 的二进制交互界面。

TUI 默认保留最近 `5000` 条完整逻辑行。滚动查看历史时，新收到的数据不会把当前阅读位置强行拉回底部。

### Hex 输入与流量时间线

TUI 接受多种便捷写法：

```text
AA 01 FF 00 7E
AA01FF007E
AA,01,FF,00,7E
0xAA 0x01 0xFF 0x00 0x7E
```

输入会实时规范化为大写、空格分隔的 Hex，并显示字节数或格式错误。发送和接收数据都进入同一时间线，每行包含 `TX`/`RX` 方向、长度、Hex 字节和可打印 ASCII。

## 串口帧配置

连接格式是：

```text
xserial <port> [cfg]
```

配置按以下顺序排列：

```text
baud,data-bits,parity,stop-bits
```

可以从右侧省略未指定的字段，省略部分使用 `115200,8,N,1` 中的对应默认值：

```bash
xserial /dev/ttyUSB0
xserial /dev/ttyUSB0 9600
xserial /dev/ttyUSB0 9600,7
xserial /dev/ttyUSB0 9600,7,E
xserial /dev/ttyUSB0 9600,7,E,2
```

- parity：`N`、`O`、`E`、`M`、`S`；
- stop bits：`1`、`1.5`、`2`；
- 默认配置：`115200,8,N,1`；
- 不支持用空字段跳过中间配置，例如 `9600,,E`。

连接断开后默认尝试重连 5 次，每次间隔 1 秒。可以指定次数，或用 `0` 禁用：

```bash
xserial /dev/ttyUSB0 --reconnect 10
xserial /dev/ttyUSB0 --reconnect 0
```

## 保存设备输出

使用 `--log` 将接收到的数据追加到文件：

```bash
xserial /dev/ttyUSB0 --log device.log
```

完全不写 `--time` 时不添加时间戳。单独传入 `--time` 时使用默认格式 `15:04:05.000`，rawui 会实时显示前缀，指定 `--log` 时同一格式也用于日志文件：

```bash
xserial /dev/ttyUSB0 --time
xserial /dev/ttyUSB0 --log device.log --time
```

需要自定义时间戳时，使用 `--time="Go 时间格式"`：

```bash
xserial /dev/ttyUSB0 \
  --log device.log \
  --time="2006-01-02 15:04:05.000"
```

时间格式遵循 Go 的 reference time 写法，也就是用固定时间 `Mon Jan 2 15:04:05 MST 2006` 的组成部分描述目标格式。下面是几种常见写法：

| `--time` 参数 | 适合场景 |
|---|---|
| `"15:04:05"` | 简洁的时分秒 |
| `"15:04:05.000"` | 带毫秒的设备调试 |
| `"2006-01-02 15:04:05"` | 易读的完整本地时间 |
| `"2006-01-02T15:04:05.000Z07:00"` | 带时区的 ISO 8601 风格时间 |
| `"Jan 02 15:04:05"` | 类似传统系统日志的格式 |

rawui 会把时间前缀插入实时显示的每个接收行；receive log 和 TUI 也使用同一格式。不写 `--time` 时，这些输出都不添加时间戳。

## Shell completion

xserial 可以为常见 Shell 生成命令补全脚本。以下命令会在当前 Shell 会话中启用补全。

### Bash

```bash
source <(xserial completion bash)
```

### Zsh

```bash
source <(xserial completion zsh)
```

### PowerShell

```powershell
xserial completion powershell | Out-String | Invoke-Expression
```

如果希望每次打开终端时自动启用，可以把对应命令加入 `~/.bashrc`、`~/.zshrc` 或 PowerShell 的 `$PROFILE`。更多补全选项可以通过下面的命令查看：

```bash
xserial completion --help
```

## 常见问题

### Linux 上提示没有串口权限

多数发行版会把串口分配给 `dialout` 或类似用户组。可以先查看设备权限：

```bash
ls -l /dev/ttyUSB0
```

然后根据发行版配置用户组。重新登录后组权限才会完全生效。

### 找不到正确的端口

先运行：

```bash
xserial list
```

如果设备刚插入，可以在插入前后分别运行一次，对比新增的端口。USB 串口通常还会显示 VID、PID、序列号或产品名称。

### 如何避免设备输出被本地提示污染？

raw 模式下，设备字节只写入 stdout；xserial 自己的帮助、上传进度和错误写入 stderr。因此默认可以安全地重定向或管道处理设备输出。显式启用 `--time` 时，stdout 会按行增加时间前缀。
