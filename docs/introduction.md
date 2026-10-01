# xserial 项目介绍

xserial 是跨平台串口终端，也提供用于二进制协议联调的工作台。可以通过 Hex 发送命令、对照 TX/RX、解释大小端字段、计算校验和、按明确规则分帧，再录制并离线复查通信过程。

```bash
go install github.com/ZhiWei-Ou/xserial/cmd/xserial@latest
xserial demo
```

无需硬件的 Demo 使用真实会话内核与模拟设备，支持正常响应、坏 CRC 和分段返回。非交互体验可运行 `xserial demo --snapshot`。

## 三种现场界面

```bash
xserial list
xserial /dev/ttyUSB0 115200
xserial /dev/ttyUSB0 --TUI
xserial /dev/ttyUSB0 --workbench
```

默认 RawUI 提供透明终端，设备字节走 stdout，本地命令和诊断走 stderr。`Ctrl-P h` 查看帮助，`Ctrl-P q` 退出。`--TUI` 是已有的全屏 Shell 终端，具有可编辑串口配置侧栏。`--workbench` 是二进制调试界面，按 Enter 才发送编辑好的 Hex。

配置位置参数为 `baud[,data-bits[,parity[,stop-bits]]]`，右侧省略使用默认值 `115200,8,N,1`。macOS 使用 `/dev/cu.*`，Windows 可直接使用 `COM3` 等端口名。

## 二进制调试与离线复查

```bash
xserial demo --frame modbus-read --record demo.xsr
xserial replay demo.xsr --frame modbus-read
xserial export demo.xsr --match "00 64"
```

工作台支持 Hex 粘贴、发送历史和命令收藏。Tab 检查字段，Ctrl-K 追加校验，Ctrl-F 搜索，Ctrl-B 标记问题，Ctrl-P 打开命令菜单。回放时 Space 暂停，`+` 和 `-` 改变速度，R 重播。

原始录制包含双方字节、实际短写结果、发送请求与连接事件；离线回放不打开串口，也不重新发送命令。详细流程见 [工作台说明](workbench.md) 和 [录制格式](capture.md)。

项目采用 MIT 许可证，开发路线见 [PLAN.md](../PLAN.md)，本地发布与用户验证材料见 [发布准备](release.md)。
