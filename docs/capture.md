# xserial 会话录制格式

`--record` 将原始串口收发和会话事件保存为版本化 JSONL，推荐使用 `.xsr` 扩展名。记录发生在串口适配器边界，早于显示转换和协议 gate；终端时间戳、Hex 展示与分帧不会改变记录的字节。

```bash
xserial /dev/ttyUSB0 --record session.xsr
xserial demo --snapshot --record demo.xsr
xserial replay demo.xsr --frame modbus-read
```

第一行包含 `format=xserial-capture`、`version=1`、开始时间与串口配置。后续每行是一个事件，包含严格递增的 `seq`、观察时间 `at`、`kind`，以及可选的 `data`、`request`、`error`、`note`。`data` 使用 JSON 标准的 Base64 字节表示。

| kind | 含义 |
| --- | --- |
| connected、disconnected、reconnecting、reconnected、closed | 连接生命周期 |
| tx_request | 经过发送 Pipeline 后提交给唯一 writer 的完整请求 |
| tx | 一次实际 Write 返回的正数字节数，短写也记录 |
| tx_complete | 完整请求得到发送确认，request 指向 tx_request 的 seq |
| tx_failed | 请求未获完整发送确认，保留错误和 request |
| rx | 一次实际 Read 返回的原始字节 |
| mark | 问题说明，at 指向被标记流量的观察时间 |

正数短写后发生错误时，已写出的字节仍作为 tx 保存，完整请求结果标记为 tx_failed。取消可能发生在物理写入之后、确认返回之前，因此分析应同时看 tx 字节与请求结果。观察时间不承诺硬件级时序精度；多任务的文件顺序由 seq 定义。

记录写入由互斥所有者串行执行，同步施加磁盘背压，不设置会丢数据的后台队列。文件写入失败作为可判定错误结束会话。串口和后台任务退出后，命令层关闭记录文件并报告关闭错误。新记录使用独占创建，既有文件不会被覆盖。

## 回放与标记

回放只在本地显示，不打开串口，也不向设备重新发送历史命令。Space 暂停，`+` 和 `-` 在 0.25 到 16 倍之间调整速度，R 重播。结束后保留界面供搜索与检查。暂停或改变速度后，当前事件间隔按新状态重新计时。

Ctrl-B 为选中流量添加标记。现场标记进入原记录；回放标记保存在 `<capture>.marks.json`，每条包含 at 和 note。源记录不被修改，损坏的已有标记文件会报错。

## 导出片段

```bash
xserial export session.xsr --from 2s --to 10s
xserial export session.xsr --match "00 64" -o matches.txt
xserial export session.xsr --from 2s --to 10s --format jsonl -o excerpt.xsr
xserial replay excerpt.xsr
```

时间范围相对于记录开始时间，含首尾；to=0 表示无上界。Hex 匹配限定在单个事件的字节内。文本输出对说明和错误转义；JSONL 导出保留源 seq 和 request，过滤后允许序号间隙，以及指向未包含请求的结果事件。侧边标记在导出中作为 mark 事件附加。

导出默认写 stdout，指定输出文件时使用独占创建。解析器拒绝损坏、截断、版本不匹配或无效序号的文件；单行限制 128 KiB，回放限制为 128 MiB 字节及说明内容和 1000000 个事件。
