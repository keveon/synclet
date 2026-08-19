# synclet

[English](README.md)

`synclet` 是一个轻量、配置驱动的 **DB polling -> DB upsert** 同步工具：

```text
reader -> mapping -> writer
```

从 source 只读轮询数据，经字段映射转换，幂等写入 target。工具本身不维护业务数据库，也不要求 source 安装代理软件。

- **PostgreSQL <-> MySQL，任意方向**：两种数据库都可作为 source 或 target——首个发布版本即同时支持 PostgreSQL -> MySQL 与 MySQL -> PostgreSQL。

> **当前状态：骨架仓库。** 包结构与契约已就位，同步引擎尚未实现——运行会返回明确的 not implemented 错误，不会静默空跑。下一步就是实现核心业务，见 [Roadmap](#roadmap)。

## 特性

- **凭据只引用环境变量名**：YAML 中只写 `connections.*.dsn_env`，不写真实 DSN、password、token。
- **结构化 SQL 配置**：reader 用 `table + alias + columns + joins + filters + cursor` 生成参数化查询；identifier 全部校验，拒绝任意 SQL。
- **JOIN 是受限能力**：仅 `inner`/`left` 的 `alias.column = alias.column` 等值关联；过滤条件只引用主表 alias。
- **字段映射**：`column / literal / json_path / json_object / selector` 五种类型，支持 `required`（缺失即报错，不用 `default`）与 `default`；selector 按序尝试，取第一个可解析数值。
- **有序 transforms**：`negative_to_zero`、`require_column_in`、`add_column` 按配置顺序执行；十进制运算统一走 `decimal`，拒绝浮点伪装精确值。
- **两种同步模式**：`snapshot`（全量拉取 + upsert，适合基础表）、`incremental`（`cursor + tie_breaker` 复合游标，适合事实表）。
- **不静默漏数**：`(cursor, tie_breaker)` 复合游标规避同时间戳组漏同步；checkpoint 仅在写入成功后推进；JOIN 先选事实批次再关联，`LIMIT` 不会截断展开行。
- **幂等 writer 与写入统计**：按 key 列 upsert；`null_update_policy: keep_existing`；JSON merge patch 列；日志区分 `attempted / inserted / updated / unchanged`。
- **安全默认**：scope 过滤 fail-closed（空 allowlist 且未显式 `allow_all: true` 即配置错误）；日志自动脱敏 DSN / token / URL userinfo；不输出 checkpoint 值或业务 payload。

## 目录结构

```text
cmd/synclet            CLI 入口
internal/              engine、reader、writer、mapping、checkpoint 等
config.example.yaml    示例配置
```

部署运行时遵循 FHS（Filesystem Hierarchy Standard）：

| 用途 | 路径 |
| --- | --- |
| 二进制 | `/usr/local/bin/synclet` |
| 配置 | `/etc/synclet/config.yaml` |
| checkpoint 状态 | `/var/lib/synclet/state.json` |

## 快速开始

前置：Go 1.26+。

```bash
git clone https://github.com/keveon/synclet.git
cd synclet

cp config.example.yaml config.yaml
# 本地试用时，把 checkpoint.path 指向可写目录。

export SOURCE_DSN='<PostgreSQL DSN>'
export TARGET_DSN='<MySQL DSN>'

# 单轮同步
go run ./cmd/synclet --config config.yaml --once

# 循环运行（默认）
go run ./cmd/synclet --config config.yaml
```

CLI 只接受长选项（`--config`、`--once`、`--version`、`--help`），单横线形式会被拒绝。

完整配置契约见 [`config.example.yaml`](config.example.yaml)——snapshot + incremental 两个 job、受限 JOIN、映射类型与 transforms。

## Roadmap

- [x] 骨架：包结构、契约、CLI、CI
- [ ] 实现核心同步引擎：`internal/engine` 编排 reader -> mapping -> writer，配套 checkpoint、config、mapping、redaction、logging
- [ ] 实现 PostgreSQL 与 MySQL 双侧的 reader/writer，首发即支持任意方向的 PostgreSQL/MySQL 组合同步
- [ ] 首个版本发布：版本 tag 与容器镜像

## 设计原则

- **fail-closed 优于静默降级**：权威字段缺失、口径未知、配置不全时直接报错，不用默认值掩盖。
- **漏数比重复更严重**：checkpoint 只在写入成功后推进；同时间戳组用复合游标消歧。
- **不信任输入**：不接受任意 SQL，identifier 全部校验，凭据不落 YAML。
- **可观测**：结构化事件日志（`start`/`read`/`complete`）、可对账的写入统计、脱敏的错误输出。

## 开发

```bash
gofmt -w ./cmd ./internal
go test ./...
go vet ./...
go build ./...
```

## 许可证

[MIT](LICENSE) © keveon
