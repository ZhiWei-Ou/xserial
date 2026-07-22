# Backend 需求

## 定位

Backend 是 xserial 的串口会话内核。它负责把一个串口连接稳定地呈现为有序的双向字节流，并管理连接、重连、并发和关闭。

Backend 不理解文本、Shell、Hex、文件传输协议或具体界面。上层能力由 [Middleware](middleware.md) 扩展，最终交互由 [RawUI](rawui.md) 或 [TUI](tui.md) 完成。

```text
serial adapter <-> backend kernel <-> middleware <-> rawui / tui
```

## 用户可感知的结果

- 用户连接成功后，可以持续收发串口数据，不因 UI 刷新、日志或后台状态提示污染设备字节。
- 设备断开后，会话不会立即消失，而是显示重连状态并允许用户主动退出。
- 重连成功后继续使用同一界面；重连耗尽后给出明确错误并正常回到终端。
- 发送失败、设备断开和程序退出都不会留下失效的 raw terminal 状态或后台任务。

## 连接与重连

### 初次连接

- Backend 根据端口名、波特率、数据位、校验位和停止位打开串口。
- 端口不存在、权限不足或配置无效时，初次连接立即失败，不进入重连流程。
- 连接成功后发出结构化的 `connected` 状态，上层决定如何展示。

### 连接后断开

- 设备断开或串口读写失败后，Backend 进入 `disconnected`，终止当前串口 I/O，并通知 Middleware。
- 默认每隔 1 秒重试同一端口，最多 5 次。
- CLI 增加 `--reconnect <count>`：
  - 默认值为 `5`；
  - `0` 表示禁用自动重连；
  - 参数必须是非负整数。
- 每次尝试都产生包含当前次数和总次数的状态；Backend 不生成面向用户的自然语言文案。
- 重连成功后进入 `connected`，重试计数清零。
- 重连次数耗尽后，会话以最后一次连接错误结束。
- 活动文件传输或其他独占操作在断线时立即失败，不自动重试或续传。

## 字节收发

### 接收

- 同一串口始终只有一个 reader。
- 每批数据带接收时间进入 Middleware，顺序与 Backend 从串口读取到的顺序一致。
- Backend 不解析编码、换行、ANSI、协议帧或文件内容。
- Backend 不主动静默丢弃已读取数据；如果上层无法继续消费，应通过背压或明确错误表现出来。
- 交付给上层的数据不能引用会被下一次读取覆盖的可变缓冲区。

### 发送

- 同一串口始终只有一个 writer，所有发送来源按进入 Backend 的顺序串行执行。
- 一次发送只有在全部字节写完后才算成功；短写必须继续写完，否则返回可判断的错误。
- 发送调用能够响应调用方取消和会话关闭。
- 未连接或正在重连时不缓存普通用户输入，直接返回明确的 disconnected 状态，避免重连后发送过期命令。

## 会话状态

Backend 至少区分以下状态：

```text
opening -> connected -> disconnected -> reconnecting -> connected
                    \-> closing -----------------------> closed
reconnecting -> exhausted -> closed
```

- 状态变化必须有确定顺序，不能重复报告互相矛盾的状态。
- `closing` 后拒绝新的发送和重连请求。
- `closed` 是终态；所有 Backend 创建的任务都已退出，串口已经关闭。

## 开发者接口要求

- 命令层传入串口配置和可替换的 port opener；Backend 自己拥有成功打开的端口生命周期。
- Backend 向 Middleware 提供：
  - 会话运行与关闭；
  - 有序发送字节；
  - 接收字节 Envelope；
  - 连接状态与致命错误事件。
- 接口使用 `context.Context` 和标准 `io` 小接口，不暴露 Cobra、Bubble Tea 或 `go.bug.st/serial` 的具体类型。
- Backend 只返回带上下文且可 unwrap 的错误，不直接打印、渲染或弹出提示。

## 并发与关闭

- 任一核心任务异常结束后，Backend 取消会话 context，关闭串口以解除阻塞 I/O，并等待自己启动的任务退出。
- 串口只有 Backend 可以关闭，关闭操作必须幂等。
- Backend 返回前不得遗留仍会访问串口、channel 或上层回调的 goroutine。
- channel 的创建者、发送者和关闭者必须明确；接收方不得关闭 Backend 创建的 channel。

## 质量要求

- 正常收发不能依赖 goroutine 的偶然调度顺序。
- 慢消费者、短写、读写失败、取消和 Close 解阻塞行为必须可由内存 fake 确定性测试。
- Backend 不依赖真实 TTY；平台串口差异继续由 serial adapter 隔离。
- 默认测试应覆盖 race detector；平台 adapter 继续进行目标平台交叉构建。

## 本轮不包含

- 文本和 ANSI 解释；
- Hex 格式化；
- raw upload、YMODEM 或其他协议算法；
- 文件记录格式与 `--log`、`--time` 的重新设计；
- 用户可配置的数据处理 Pipeline；
- 外部插件加载。

