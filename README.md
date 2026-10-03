# HL7 v2.5.1 检验结果归档服务

接收 LIS 通过 MLLP/TCP 发送的 ORU^R01 检验结果消息，按状态机规则归档到
SQLite，并提供 HTTP 只读查询接口。仅后端，无前端。

## 范围

- **接收**：MLLP/TCP（VT 0x0B … FS 0x1C CR 0x0D 帧），CR 分段，固定分隔符
  `|^~\&`，仅 ORU^R01。支持半包（跨 TCP 段）与同一连接连续多帧，单帧上限 1 MiB。
- **消息结构**：每消息恰好一个 PID、一个 OBR、1–20 个 OBX；其余段忽略，非法即拒（AE）。
- **标识**：来源 = MSH-3 ^ MSH-4；病人 = PID-3.1；检验单 = OBR-3；
  项目身份 = OBX-3.1 编码 + OBX-3.3 编码体系 + OBX-4 子 ID（不含显示名 OBX-3.2）。
- **值**：仅 NM（十进制字符串原样存储）与 ST（解码 \F\ \S\ \R\ \T\ \E\
  转义，未知转义拒绝）；单位取 OBX-6.1 保留。
- **状态机**（OBX-11 仅 P/F/C）：
  - 新项目可 P 或 F；C 必须已有确认（F）结果。
  - P 可被 P 更新、可升 F。
  - F（及更正后）只能由 C 改值；同值重报为无操作。
  - 消息未携带的项目不删除；当前值与全部变化历史均保存。
- **去重**：以（来源, MSH-10）为键。同 ID 同原报文重送 → 直接 AA 不产生新版本；
  同 ID 异内容 → AE 拒绝。结果、历史、去重记录在同一事务提交；全部成功回 AA，
  任一失败回 AE（MSA-2 关联原 MSH-10），绝不部分写入。SQLite 单写者 +
  唯一约束保证并发重送只生效一次；WAL 持久化，重启后可续收与查询。
- **串单防护**：检验单首次绑定病人，之后同（来源, 检验单）异病人即拒；
  消息内重复项目即拒。
- **资源管理**：MLLP 连接数上限（默认 32，超出即拒）、每帧读超时 30s、
  写超时 10s；HTTP 读/写/空闲超时；SIGINT/SIGTERM 优雅关闭，等待在途连接并关闭 DB。

## 构建与运行

    go build -o bin/server ./cmd/server
    ./bin/server -mllp-addr :2575 -http-addr :8080 -db data/lab.db -max-conns 32

## HTTP 查询

- `GET /api/sources/{source}/orders/{order}/results` — 当前结果
- `GET /api/sources/{source}/orders/{order}/history` — 全部版本历史
- `GET /healthz`

`source` 为 `MSH-3^MSH-4`，URL 中 `^` 需编码为 `%5E`，例如：

    curl 'http://localhost:8080/api/sources/LIS%5EHOSP-A/orders/ORD-9001/results'

## 演示

`examples/` 内置虚构消息（预检 P、确认 F、更正 C、补报项目及四类非法消息）。
一键演示（构建、启动、经 MLLP 客户端发送、curl 查询）：

    bash scripts/demo.sh

手动发送单条消息：

    go run ./cmd/mllp-send -addr localhost:2575 examples/01-preliminary.hl7

重启持久化验证：`bash scripts/restart-test.sh`

## 测试

    go test ./...

覆盖解析/转义/非法消息拒绝（internal/hl7）与状态机、去重、串病人、
历史版本（internal/store）。

## 结构

- `internal/mllp` — MLLP 帧传输、连接限制与读期限
- `internal/hl7` — 消息解析、转义解码、ACK 构建
- `internal/ingest` — 接收编排：解析 → 入库 → ACK
- `internal/store` — SQLite 仓储：状态机、事务、去重、查询
- `internal/httpapi` — Chi 查询路由
- `cmd/server` — 服务入口（信号处理、优雅关闭）
- `cmd/mllp-send` — 演示用 MLLP 客户端
