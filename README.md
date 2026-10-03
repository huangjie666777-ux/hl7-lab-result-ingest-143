# HL7 v2.5.1 检验结果归档后端

接收 LIS 通过 MLLP/TCP 发来的 ORU^R01 检验结果，按状态机规则归档当前值与全部变化历史，并提供 HTTP 查询。无前端。

## 范围

- **传输**：MLLP over TCP（VT 0x0B … FS 0x1C CR），CR 分段，固定分隔符 |^~\&。支持半包（跨多次 read 拼帧）与同一连接连续多帧，单帧上限 1 MiB，超限断连。连接数上限 64，读超时 30s，写超时 10s，关闭时回收连接与句柄。
- **消息**：每消息恰好 1 个 MSH/PID/OBR，1–20 个 OBX；其余段拒绝。来源 = MSH-3^MSH-4；病人 = PID-3.1；检验单 = OBR-3；项目身份 = OBX-3.1(编码)+OBX-3.3(体系)+OBX-4(子ID)，不含显示名称。消息内重复项目、检验单串病人均拒绝。
- **值**：仅 NM（十进制字符串原样校验存储）与 ST（解码 \F\ \S\ \R\ \T\ \E\，未知转义拒绝），保留 OBX-6 单位。任何字段缺失/非法导致整消息拒绝，绝不部分写入。
- **状态机**（OBX-11 仅 P/F/C）：新项目可 P 或 F；P 可被 P 更新、被 F 升级；F（含更正后）只能由 C 改值，C 必须已有确认结果；F 原值重发视为无操作。未携带的项目保持不变，不删除。
- **去重**：按（来源, MSH-10）去重。同 ID 同原报文重送回 AA 且不新增版本；同 ID 异内容回 AE。消息、结果、历史、去重记录同一事务提交；并发重送由唯一约束保证只生效一次。SQLite(WAL) 持久化，重启后可续收与查询。
- **ACK**：提交成功回 AA，任何失败回 AE（MSA-3 带原因），MSA-2 关联原 MSH-10。

## 构建与启动

```sh
go build ./...          # 或 go test ./...
go build -o bin/server ./cmd/server
go build -o bin/mllp-send ./cmd/mllp-send
./bin/server            # 环境变量: MLLP_ADDR(:2575) HTTP_ADDR(:8080) DB_PATH(lab.db)
```

## HTTP 查询

- `GET /api/results?source=LIS%5EHOSP-A&order=ORD9001` — 当前结果
- `GET /api/results/history?source=LIS%5EHOSP-A&order=ORD9001` — 全部历史版本
- `GET /healthz`

source 为 `MSH-3^MSH-4`（`^` 需 URL 编码为 %5E）。

## 演示（虚构消息见 demo/）

```sh
./bin/mllp-send demo/msg1_preliminary.hl7        # 补报 P -> AA
./bin/mllp-send demo/msg2_final.hl7              # 确认 F -> AA
./bin/mllp-send demo/msg3_correction.hl7         # 更正 C -> AA
./bin/mllp-send demo/msg4_invalid_downgrade.hl7  # F 后回退 P -> AE
./bin/mllp-send demo/msg1_preliminary.hl7        # 同 ID 同内容重送 -> AA，不新增版本
curl -s 'http://127.0.0.1:8080/api/results?source=LIS%5EHOSP-A&order=ORD9001'
curl -s 'http://127.0.0.1:8080/api/results/history?source=LIS%5EHOSP-A&order=ORD9001'
```

## 结构

- `internal/mllp` — MLLP 监听、分帧、连接限制与超时
- `internal/hl7` — 消息解析/校验、ST 转义解码、ACK 构造
- `internal/domain` — P/F/C 状态迁移规则
- `internal/store` — SQLite 仓储：事务化 ingest、去重、查询
- `internal/httpapi` — chi 查询路由
- `cmd/server` / `cmd/mllp-send` — 服务端与演示客户端

