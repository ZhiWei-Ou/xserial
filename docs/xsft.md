# XSerial File Transfer Protocol

状态：Draft 0.2  
简称：XSFT

## 1. 目标

XSFT（XSerial File Transfer）是运行在串口上的单文件传输协议，供 MCU 命令行与 xserial 配合使用。用户体验类似 `rz`、`sz`，但协议与 ZMODEM 不兼容。

协议支持：

- xserial 自动识别设备发起的 `rz`、`sz` 请求；
- 主机文件选择框或保存对话框；
- 流式读写、帧校验、重传、取消和超时；
- 一个连接同时只传输一个文件。

协议不支持多文件、断点续传、滑动窗口、压缩、加密和身份认证。

## 2. 模块边界

XSFT 是通用协议模块，只负责编解码帧、校验、重传和传输状态。

- 设备适配层负责独占 console、执行 `rz`/`sz` 命令和读写文件；
- xserial 负责识别请求、打开文件对话框和读写主机文件；
- 接收方负责检查文件名、目标路径、覆盖策略和剩余空间。

## 3. 传输模型

串口必须提供全双工、有序字节流。接收方必须缓存并解析跨多次读取到达的帧。

协议采用停等传输：发送方发出一个需要确认的帧后，必须等待 ACK，超时则重发完全相同的帧。任何时刻最多只有一个帧等待确认，因此不需要会话编号和帧序号。

重复帧按当前传输状态处理：

- 重复的控制帧只重发 ACK，不重复执行打开或关闭文件等操作；
- DATA 通过 `offset` 判断是否已经写入；
- 与当前状态无关的过期帧回复 `ACK(INVALID)` 并结束传输。

设备独占 console 后才启动 XSFT；传输结束并清空串口接收缓存后再归还 console。

## 4. 数据约定

- 多字节整数使用小端字节序；
- 文件大小和偏移使用无符号 64 位整数；
- CRC 使用 CRC-32/ISO-HDLC；
- 文件名使用 UTF-8 basename，长度为 1..255 字节，不得包含 `/`、`\` 或 NUL，也不得为 `.` 或 `..`；
- 单帧 payload 不得超过 1032 字节，DATA 内容不得超过 1024 字节。

## 5. 帧格式

```text
偏移  大小  字段
0     4     magic = "XSFT" = 58 53 46 54
4     1     type
5     2     payload_length
7     N     payload
7+N   4     frame_crc32
```

`frame_crc32` 覆盖 `type`、`payload_length` 和 payload，不包含 magic 和 CRC 字段自身。

解析步骤：

1. 搜索 magic；
2. 检查 type 和 payload 长度；
3. 等待完整帧并校验 CRC；
4. 校验失败时丢弃当前 magic 的第一个字节，再继续搜索；
5. 只有 CRC 正确的 `RZ_REQUEST` 或 `SZ_REQUEST` 才能触发文件对话框。

## 6. 帧类型

| 值 | 名称 | 方向 | 用途 |
|---:|---|---|---|
| `0x01` | `RZ_REQUEST` | 设备 → 主机 | 请求接收主机文件 |
| `0x02` | `SZ_REQUEST` | 设备 → 主机 | 请求向主机发送文件 |
| `0x03` | `WAIT` | 主机 → 设备 | 文件对话框仍在等待用户 |
| `0x04` | `READY` | 主机 → 设备 | 已准备接收设备文件 |
| `0x05` | `FILE_INFO` | 发送方 → 接收方 | 声明文件信息 |
| `0x06` | `DATA` | 发送方 → 接收方 | 传输文件内容 |
| `0x07` | `FINISH` | 发送方 → 接收方 | 提交文件大小和 CRC |
| `0x08` | `ACK` | 双向 | 确认帧或报告错误 |
| `0x09` | `CANCEL` | 双向 | 取消传输 |

除 ACK 外的帧都需要 ACK。未知类型回复 `ACK(UNSUPPORTED)`。

## 7. Payload

### 7.1 RZ_REQUEST 与 SZ_REQUEST

```text
大小  字段
2     max_data_length，设备支持的最大 DATA 内容长度，1..1024
```

设备执行 `rz` 或 `sz <file>` 后周期性发送请求，直到收到 ACK、用户取消或启动超时。`sz` 必须先确认源文件可以打开，但文件信息统一由后续 FILE_INFO 传递。

主机收到请求并接受后回复 `ACK(OK)`，然后打开对应的文件对话框。已有传输时回复 `ACK(BUSY)`。

### 7.2 WAIT 与 READY

WAIT 和 READY 的 payload 均为空。

文件对话框打开期间，主机每 1000 ms 发送 WAIT。WAIT 只用于保活，不推进传输。用户在 `sz` 保存对话框中选定目标并成功创建文件后，主机发送 READY。

### 7.3 FILE_INFO

```text
大小  字段
2     data_length，建议的 DATA 内容长度，1..1024
2     name_length
8     file_size
N     file_name，UTF-8 basename
```

接收方检查文件信息、目标路径、空间和覆盖策略后回复 ACK。成功 ACK 通过 `accepted_data_length` 返回最终 DATA 长度，且不得超过双方上限。

### 7.4 DATA

```text
大小  字段
8     offset
N     data，1..accepted_data_length 字节
```

第一个 offset 为 0，后续 offset 必须等于接收方期待的偏移。接收方成功写入 data 后才回复 ACK，并在 `next_offset` 中返回下一期待偏移。

当 offset 小于 `next_offset` 且该帧结束位置等于 `next_offset` 时，视为 ACK 丢失导致的重复 DATA：不得再次写入，只重发成功 ACK。其他偏移回复 `ACK(BAD_OFFSET)` 并结束传输。

### 7.5 FINISH

```text
大小  字段
8     file_size
4     file_crc32
```

接收方仅在累计写入大小、FILE_INFO 大小、FINISH 大小和整文件 CRC 全部一致，且文件同步、关闭成功后回复 `ACK(OK)`。

接收方宜先写临时文件，校验成功后再替换目标文件。文件系统无法可靠替换时，可直接写目标文件，但取消或失败后可能留下不完整文件。

### 7.6 ACK

```text
大小  字段
1     acked_type
1     status
2     accepted_data_length，仅 FILE_INFO 成功时非零
8     next_offset，仅 DATA 成功或 BAD_OFFSET 时使用
```

| 值 | 名称 | 含义 |
|---:|---|---|
| `0x00` | `OK` | 已成功处理 |
| `0x01` | `BUSY` | 当前已有传输 |
| `0x02` | `DECLINED` | 用户拒绝或不允许覆盖 |
| `0x03` | `INVALID` | payload 或状态不合法 |
| `0x04` | `IO_ERROR` | 文件操作失败 |
| `0x05` | `NO_SPACE` | 空间不足 |
| `0x06` | `BAD_OFFSET` | DATA 偏移错误 |
| `0x07` | `BAD_CHECKSUM` | 文件 CRC 错误 |
| `0x08` | `UNSUPPORTED` | 帧类型或能力不支持 |

除请求收到 BUSY 外，发送方收到非 OK ACK 后必须结束传输。

### 7.7 CANCEL

```text
大小  字段
1     reason
```

| 值 | 名称 |
|---:|---|
| `0x00` | `USER` |
| `0x01` | `TIMEOUT` |
| `0x02` | `PROTOCOL_ERROR` |
| `0x03` | `IO_ERROR` |
| `0x04` | `SHUTDOWN` |

收到 CANCEL 后立即停止读写、关闭文件、回复 ACK，并释放传输资源。发送方收到 ACK 或重试耗尽后结束传输。

## 8. 传输流程

### 8.1 `rz`：主机发送文件

```text
设备                         主机
  |-- RZ_REQUEST ------------->|
  |<------------- ACK(OK) ------|
  |<---------------- WAIT ------|  用户选择文件期间周期发送
  |-- ACK(OK) ----------------->|
  |<------------- FILE_INFO ----|
  |-- ACK(OK, length) --------->|
  |<---------------- DATA ------|
  |-- ACK(OK, next_offset) ---->|
  |              ...            |
  |<-------------- FINISH ------|
  |-- ACK(OK) ----------------->|
```

设备命令为 `rz [destination]`。未指定 destination 时，以 FILE_INFO 的 basename 保存到当前目录。

### 8.2 `sz`：设备发送文件

```text
设备                         主机
  |-- SZ_REQUEST ------------->|
  |<------------- ACK(OK) ------|
  |<---------------- WAIT ------|  用户选择目标期间周期发送
  |-- ACK(OK) ----------------->|
  |<--------------- READY ------|
  |-- ACK(OK) ----------------->|
  |-- FILE_INFO -------------->|
  |<------ ACK(OK, length) ------|
  |-- DATA ------------------->|
  |<------ ACK(OK, next_offset)-|
  |              ...            |
  |-- FINISH ----------------->|
  |<------------- ACK(OK) ------|
```

设备命令为 `sz <source>`。

用户取消对话框时，主机发送 `CANCEL(USER)`，不得静默停止保活。

## 9. 超时与重传

| 参数 | 默认值 | 行为 |
|---|---:|---|
| 请求周期 | 500 ms | 未收到 ACK 时重发请求 |
| 启动超时 | 30 s | 请求未被接受则退出命令 |
| WAIT 周期 | 1000 ms | 对话框打开期间由主机发送 |
| WAIT 失活超时 | 3000 ms | 设备取消传输并释放 console |
| ACK 超时 | 1000 ms | 重发当前帧 |
| 最大重试次数 | 5 | 发送 CANCEL(TIMEOUT) 后结束 |
| 空闲超时 | 5000 ms | 非对话框状态无有效帧时取消 |

CRC 错误帧不刷新超时。设备不能无限等待主机，所有退出路径最终都必须释放 console。

## 10. 实现要求

设备端：

- console 独占成功后才发送请求，所有退出路径都释放 console；
- DATA 写入成功后才 ACK，不重复写入重传 DATA；
- 逐帧更新整文件 CRC，不把整个文件载入 RAM；
- 检查声明大小、累计大小、偏移和整数溢出；
- 支持本地取消，并向主机发送 CANCEL；
- FINISH 成功后才报告命令成功。

xserial：

- 仅用 CRC 正确的请求触发文件对话框；
- 对话框存续期间周期性发送 WAIT，用户取消时发送 CANCEL；
- 流式读写文件，写入成功后才 ACK；
- 校验 FINISH 的大小和整文件 CRC；
- 连接或进程关闭时尽可能发送 CANCEL(SHUTDOWN)。
