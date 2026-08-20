# synclet

[English](README.md)

`synclet` 是一个轻量、配置驱动的数据库表数据同步工具：

```text
source 数据库 -> 读取 -> 映射 -> upsert -> target 数据库
```

它对 source 数据库只读轮询——source 侧无需安装任何组件——按配置映射转换字段后，幂等写入 target。工具本身不维护业务数据库。

- **PostgreSQL 与 MySQL，任意方向**：两种数据库都可作为 source 或 target，包括任意方向的混合组合同步。

## 特性

- **凭据不落配置文件**：连接只引用环境变量名（`connections.*.dsn_env`）——YAML 中不出现 DSN、password、token。
- **不接受任意 SQL**：读取由结构化配置（`table`、`columns`、`joins`、`filters`、`cursor`）生成，identifier 全部校验；JOIN 仅限 inner/left 等值关联。
- **两种同步模式**：`snapshot`（每轮全量拉取 + upsert，适合基础表）与 `incremental`（`cursor + tie_breaker` 复合游标分页，适合事实表）。
- **不静默漏数**：`(cursor, tie_breaker)` 复合游标规避同时间戳组漏同步；checkpoint 仅在写入成功后推进。
- **字段映射**：`column / literal / json_path / json_object / selector` 五种类型，支持 `required` 与 `default`；transforms 按配置顺序执行，十进制运算精确——浮点值会被拒绝，而不是伪装成精确值。
- **幂等写入与统计**：按 key 列 upsert，`null_update_policy: keep_existing`，JSON merge patch 列；日志区分 `attempted / inserted / updated / unchanged`。
- **fail closed**：不完整或有歧义的配置直接报错——空 allowlist 且未显式 `allow_all: true` 时拒绝运行。
- **可观测、可放心记日志**：结构化、可 grep 的事件日志；DSN、token 与 URL userinfo 自动脱敏。

## 快速开始

前置：Go 1.26+。

```bash
git clone https://github.com/keveon/synclet.git
cd synclet

cp config.example.yaml config.yaml   # 按环境调整连接、job 与映射
export SOURCE_DSN='<PostgreSQL DSN>'
export TARGET_DSN='<MySQL DSN>'

# 单轮同步
go run ./cmd/synclet --config config.yaml --once

# 持续运行（默认）
go run ./cmd/synclet --config config.yaml
```

`synclet --help` 列出全部选项。完整配置契约——snapshot + incremental 两个 job、受限 JOIN、映射类型与 transforms——见 [`config.example.yaml`](config.example.yaml)。

## 容器镜像

每个 release tag 都会发布多平台镜像（linux/amd64 + linux/arm64）到 GHCR：

```bash
docker run --rm ghcr.io/keveon/synclet --version
```

挂载配置 + 环境变量传凭据运行（完整契约见 `config.example.yaml`）：

```bash
docker run --rm \
  -v $PWD/config.yaml:/etc/synclet/config.yaml:ro \
  -v synclet-data:/var/lib/synclet \
  --env-file .env \
  ghcr.io/keveon/synclet --once
```

说明：

- 容器以专用非 root 用户（uid 65532）运行；bind mount 的 checkpoint 目录需在宿主机 `chown 65532:65532`。
- 从仓库根目录本地构建：`docker build -t synclet:dev .`

## 运行时路径

部署运行时遵循 FHS（Filesystem Hierarchy Standard）：

| 用途 | 路径 |
| --- | --- |
| 二进制 | `/usr/local/bin/synclet` |
| 配置 | `/etc/synclet/config.yaml` |
| checkpoint 状态 | `/var/lib/synclet/state.json` |

## Roadmap

- [x] CLI、配置契约、CI
- [x] 核心同步引擎：reader -> mapping -> writer、checkpoint、调度
- [x] 首个版本发布：版本 tag 与容器镜像

## 原则

- **漏数比重复更严重**——checkpoint 只在写入成功后推进。
- **宁可报错，不做猜测**——配置缺失或有歧义时直接失败，不用默认值掩盖。
- **不信任输入**——identifier 全部校验、查询全部参数化、密钥不落文件。

## 开发

```bash
gofmt -w ./cmd ./internal
go test ./...
go vet ./...
go build ./...
```

## 许可证

[MIT](LICENSE) © keveon
