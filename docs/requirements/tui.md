# 全屏终端需求

`xserial <port> [cfg] --TUI` 保留已有的文本终端交互。普通按键立即发往设备，接收侧保留 ANSI SGR 色彩并解释常用光标、擦除和滚动控制。完整 VT 仿真不作为承诺。

界面使用黑色背景和橙色边框，侧栏展示串口配置，底部展示连接状态与统计。配置字段可以点击或通过 `Ctrl-P c` 聚焦；确认修改后更换连接。端口选择弹窗打开期间持续刷新，支持方向键与 h/j/k/l。

`Ctrl-P` 打开命令菜单，连续两次发送 literal prefix。`Shift-PageUp` 和 `Shift-PageDown` 浏览终端历史。保持设备终端按键语义，退出和错误路径恢复终端，运行期间不向 stderr 插入后台日志来破坏 alternate screen。

二进制协议调试使用独立的 `--workbench` 入口，其 Hex 编辑、TX/RX 流量、字段检查、分帧、录制回放和快捷键契约见 [工作台说明](../workbench.md)。`--TUI`、`--workbench` 与 `--hexdump` 互斥。
