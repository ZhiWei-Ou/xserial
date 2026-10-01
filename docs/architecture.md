# xserial 运行时架构

xserial 采用按职责分包的模块化单体。RawUI 提供字节透明终端，TUI 保留全屏 Shell 交互，workbench 提供二进制调试。三种现场前端共享串口生命周期、唯一 writer 与传输编排；离线回放复用 workbench，不打开串口。

## 依赖与职责

```text
cmd/xserial -> internal/cmd
                 | 组装前端、串口、配置、记录文件
                 v
             middleware -> backend -> serialport / demo
                 |             |
                 v             v
             transfer       capture.Port
                 |
         rawui / tui / workbench
                         |
                         v
                    hexdata / capture
```

- `internal/cmd` 定义 Cobra 命令、校验参数并组装依赖，负责文件的最终关闭与错误呈现。
- `internal/backend` 管理物理连接、单 reader 与单 writer、重连、配置替换及关闭。ownedPort 保证每个物理串口只 Close 一次。
- `internal/middleware` 编排前端与 backend，运行双向 Handler Pipeline 和 YMODEM 独占 gate，记录请求与连接事件。
- `internal/rawui` 处理 raw mode、prefix 和透明输入输出；`internal/tui` 处理已有的 Shell 终端画面与串口配置。
- `internal/workbench` 处理 Hex 编辑、命令收藏、显示历史、字段选择、校验预览、搜索、标记与回放控制。
- `internal/hexdata` 提供确定性的 Hex 解析、字节解释、校验和流式分帧算法，不依赖 Cobra 或终端库。
- `internal/capture` 记录原始读写结果、编码 JSONL、加载回放记录与导出片段。文件持有者由命令层指定。
- `internal/demo` 实现模拟串口的阻塞读取、查询响应与关闭契约，复用真实会话内核。

## 现场数据流

```text
设备 -> capture.Port 原始 RX 观察 -> backend
     -> middleware inbound Pipeline -> frontend

frontend -> middleware outbound Pipeline -> tx_request
         -> backend 唯一 writer -> capture.Port 原始 TX 观察 -> 设备
         -> tx_complete 或 tx_failed -> frontend 获得发送结果
```

capture.Port 只观察原始字节与实际 I/O 结果，不转换设备数据。普通前端发送与协议发送都复用唯一 writer，短写通过 WriteFull 继续发送；部分字节已写出但整体失败时，记录保留实际 tx 和失败结果。

middleware Envelope 包含 Data、Direction、At、Source。Inbound 按注册顺序执行，Outbound 按逆序执行。Handler 可以转发、变换或显式消费字节；YMODEM gate 获取双向独占权，将协议应答交给传输任务并阻止普通键盘发送。框架只在项目代码中组装，不提供外部插件市场。

工作台分帧只影响展示，原始录制发生在 Pipeline 与分帧之前。未配置规则时显示数据块；配置后在完整数据到齐时显示帧。断线清空未完成缓冲，历史和原始记录保留。

## 录制与离线回放

记录格式以 Header 开头，后续按严格递增 seq 保存事件，字节使用 Base64。Writer 用互斥锁串行写入，同步施加背压；写入错误可通过 ErrRecording 判断，并结束会话。它不持有串口，也不自行打印错误。

回放直接读取 capture.Session，通过可取消定时器把记录交给工作台。模型拥有暂停、倍速、重播和过期定时器的 generation；不存在重新发送到设备的路径。回放问题标记写入 sidecar，导出时带上说明。

## 输出与关闭

默认 RawUI 的设备字节只写 stdout，本地 UI 和诊断只写 stderr；显式时间戳或 Hexdump 仅改变所选显示。TUI 和 workbench 占用 stdout 的 alternate screen，会话 logger 在这两种模式下关闭。

```text
任务结束、用户退出或错误
  -> cancel context
  -> Close port 解除阻塞 I/O
  -> 等待 backend、dispatcher、transfer 与 frontend 收敛
  -> 停止 Pipeline 并关闭事件通道
  -> 恢复终端
  -> 写入记录结束事件并关闭文件
  -> 返回错误
```

后台任务使用明确等待机制。工作台另外跟踪 Bubble Tea 派发的命令：退出时取消读取与回放定时器，等待已开始的文件或发送操作，拒绝关闭后才开始的操作。启动阶段取消同样等待后台退出并关闭资源。

## 验证边界

纯算法覆盖 Hex 输入、错误位置、大小端、数值解释、已知 CRC 向量及半帧与粘连。前端测试覆盖完整编辑与发送流程、失败、历史与滚动、收藏、字段检查、搜索和回放控制。

内存串口覆盖阻塞读取、短写、关闭解阻塞、重连与 Demo 场景。记录测试覆盖原始字节、请求关联、并发写入、文件错误和导出。真实 TTY 体验单独验证；默认测试不依赖物理串口。

验证命令为 `go test ./...`、`go test -race ./...` 和 `make all` 的六个目标平台构建。
