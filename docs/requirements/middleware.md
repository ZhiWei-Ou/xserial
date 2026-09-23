# Middleware 需求

## 定位

Middleware 位于 [Backend](backend.md) 与前端之间，为串口字节流提供可组合、可扩展的处理能力。

它不是传统 Web 请求中间件，也不是全局事件总线。每个串口会话拥有一条独立的双向 Handler Pipeline：

```text
Inbound : backend -> H1 -> H2 -> H3 -> rawui / tui
Outbound: rawui / tui -> H3 -> H2 -> H1 -> backend
```

Pipeline 面向开发者扩展。最终用户不需要理解、配置或管理 Handler。

## 设计目标

- 新增记录、统计、编解码、触发器或文件传输时，不扩大会话内核和 UI 接口。
- 所有能力复用 Backend 的唯一 reader 和 writer，不为协议另开串口读写循环。
- 处理顺序、字节所有权、背压、取消和错误传播可以被明确说明和测试。
- 支持运行期临时接管串口，同时保证能力结束后普通终端流可以恢复。
- 保持 Go 风格的显式组装，不引入依赖注入容器、动态库或预测式插件框架。

## Pipeline 交互模型

### Envelope

主 Pipeline 只传播字节 Envelope。Envelope 至少表达：

- 字节内容；
- inbound 或 outbound 方向；
- 数据产生时间；
- 来源，例如 serial、frontend 或 handler。

Envelope 不携带任意 `any` 业务对象。Handler 产生的进度、状态、解析结果和错误通过类型化旁路事件交给上层。

Handler 若要保留或异步处理数据，必须复制字节；不能持有随后可能被复用的缓冲区。

### Handler 行为

一个 Handler 可以只实现 inbound、只实现 outbound，或同时处理两个方向。收到 Envelope 后可以：

- 原样继续传播；
- 生成替换后的 Envelope 再传播；
- 消费当前 Envelope，阻止其到达下一层；
- 发出类型化事件；
- 通过 Handler context 发起反向写入；
- 返回错误，结束当前能力或会话。

是否继续传播必须显式表达，不能因为遗漏调用而静默丢失数据。Pipeline 会跳过未实现当前方向的 Handler。

### 顺序

- inbound 按 Handler 注册顺序执行；outbound 按相反顺序执行。
- 同一方向的数据按进入 Pipeline 的先后顺序处理。
- Handler 的注册顺序属于行为契约，composition root 必须使用稳定名称显式组装。
- 同名 Handler 不能重复注册。

## Handler 生命周期

- 常驻 Handler 在会话启动时按顺序激活，在会话关闭时按相反顺序停止。
- 临时 Handler 可以在运行期添加、移除或替换。
- Pipeline 的结构变化必须通过 Pipeline owner 提交，并与数据事件在同一串行执行上下文中生效。
- Handler 激活失败时不得留下半注册状态。
- Handler 移除前先停止接收新事件，再取消并等待它启动的后台任务。
- 会话关闭后拒绝任何 Pipeline 结构变化。

## 能力与占用

Handler 声明自己对串口方向的占用方式：

| 能力 | Inbound | Outbound | 典型用途 |
|---|---|---|---|
| Passive | 继续传播 | 继续传播 | 统计、未来的记录 |
| Inbound consume | 消费 | 继续传播 | 接收侧解析或触发器 |
| Outbound exclusive | 继续传播 | 独占 | 单向发送独占 |
| Duplex exclusive | 独占 | 独占 | YMODEM 等双向协议 |

- 同一方向同时只能存在一个 exclusive Handler。
- 独占申请失败时立即返回 busy，不排队等待，也不抢占当前能力。
- Outbound exclusive 激活期间，普通前端发送被明确拒绝；Inbound 仍可以继续到达前端。
- Duplex exclusive 激活期间，协议字节只交给独占 Handler，不进入终端输出。
- 独占 Handler 完成、取消、失败或断线后必须释放占用并从 Pipeline 移除。

## 文件传输能力

### YMODEM

- 协议算法继续作为独立 transfer 模块，Middleware 用 duplex-exclusive Handler 接入。
- Handler 消费 YMODEM 协议字节，并通过同一 outbound Pipeline 发送响应。
- 支持上传、下载、取消、重试信息、CRC32 和完成统计。
- 协议完成或失败后恢复普通终端数据流。
- 断线时立即失败，不自动续传。

## 错误与背压

- 临时 Handler 的业务错误默认只结束该 Handler，发出失败事件并释放占用；Backend 会话保持可用。
- 常驻基础 Handler 无法继续传播数据时，Pipeline 结束会话并把错误返回 Backend owner。
- Handler 不得只记录错误后假装成功。
- Pipeline 默认有序处理并施加背压，不静默丢弃 Envelope。
- 耗时文件 I/O 或协议任务应在受控 worker 中运行，通过 Pipeline context 交互，不能长期阻塞数据事件循环。
- 旁路事件消费者过慢时必须有显式的限流、合并或失败策略；进度事件可以合并，完成与错误事件不能丢失。

## 开发者组装体验

- Handler 在 Go composition root 中以代码注册和排序。
- 新增 Handler 只需依赖 Middleware 的 Envelope、Handler context 和类型化事件契约，不依赖 rawui、tui、Cobra 或具体串口库。
- 测试可使用内存 Pipeline context 验证单个 Handler，不需要真实串口。
- 第一阶段不提供配置文件排序、脚本扩展、Go plugin 或第三方插件市场。

## 开源设计参考

- [Netty ChannelPipeline](https://netty.io/4.1/api/io/netty/channel/ChannelPipeline.html)：双向 Handler、相反的 inbound/outbound 顺序、显式事件传播和运行期增删。
- [Apache MINA Filters](https://mina.apache.org/mina-project/userguide/ch5-filters/ch5-filters.html)：选择性覆盖事件、日志/codec 等横切能力，以及转换后保持发送语义。
- [Tower ServiceBuilder](https://docs.rs/tower/latest/tower/struct.ServiceBuilder.html)：有序组合、ready/backpressure 和可复用 Layer。
- [OpenTelemetry Collector Pipeline](https://opentelemetry.io/docs/collector/architecture/)：按角色组装 Pipeline，同时提醒同步处理器阻塞会向整条链扩散。

本项目只吸收这些设计中的边界、顺序和生命周期思想，不复制其通用网络框架规模。

## 验收场景

- Passive Handler 同时观察 RX/TX，但不改变最终字节。
- Transform Handler 修改字节后，下游只收到修改后的 Envelope。
- Consume Handler 阻止当前 Envelope 继续传播，且行为可观察。
- 多个 Handler 的 inbound/outbound 顺序与定义一致。
- YMODEM 独占双向时，协议字节不泄漏到 rawui 或 tui。
- 取消、Handler 错误和设备断线都会移除临时 Handler，并恢复或结束到确定状态。
- Handler 动态增删与高频数据同时发生时不存在竞态、乱序或遗留 goroutine。

## 本轮不包含

- 面向用户的 Pipeline 配置；
- 通用业务对象 Pipeline；
- 任意 fan-out 数据图；
- 运行时加载外部二进制插件；
- 记录格式和 `--log`、`--time` 的重新设计。

