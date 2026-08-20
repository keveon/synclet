# synclet

[简体中文](README.zh-CN.md)

`synclet` is a lightweight, config-driven tool that replicates table data between databases:

```text
source DB -> read -> map -> upsert -> target DB
```

It polls a source database read-only — nothing to install on the source side — maps and transforms fields on the fly, and upserts into the target. It manages no business schema of its own.

- **PostgreSQL and MySQL, any direction**: both engines work as source and target, including mixed pairings in either direction.

## Features

- **Credentials never touch config files**: connections reference environment variable names (`connections.*.dsn_env`) — no DSNs, passwords or tokens in YAML.
- **No arbitrary SQL**: reads are built from structured config (`table`, `columns`, `joins`, `filters`, `cursor`) with every identifier validated; JOINs are restricted to inner/left equi-joins.
- **Two sync modes**: `snapshot` (full pull + upsert each round, for reference tables) and `incremental` (composite `cursor + tie_breaker` keyset paging, for fact tables).
- **No silent row loss**: the composite `(cursor, tie_breaker)` cursor prevents same-timestamp groups from being skipped; the checkpoint advances only after a successful write.
- **Field mapping**: `column / literal / json_path / json_object / selector` types with `required` and `default`; ordered transforms with exact decimal arithmetic — floats are rejected rather than masquerading as exact. Selectors come in two flavors: `json_path` (rooted dot path) and `element` (resolve an entry by its `code` — from a code-keyed object or an array of code-tagged entries — then resolve `value_path` inside it).
- **Idempotent writes with stats**: upsert by key columns, `null_update_policy: keep_existing`, JSON merge-patch columns; logs distinguish `attempted / inserted / updated / unchanged`.
- **Fail closed**: an incomplete or ambiguous config is an error — an empty allowlist without an explicit `allow_all: true` refuses to run.
- **Observable and safe to log**: structured, greppable event logs; DSNs, tokens and URL userinfo are redacted automatically.

## Quick start

Prerequisites: Go 1.26+.

```bash
git clone https://github.com/keveon/synclet.git
cd synclet

cp config.example.yaml config.yaml   # adjust connections, jobs and mappings
export SOURCE_DSN='<PostgreSQL DSN>'
export TARGET_DSN='<MySQL DSN>'

# single pass
go run ./cmd/synclet --config config.yaml --once

# continuous mode (default)
go run ./cmd/synclet --config config.yaml
```

`synclet --help` lists all options. The full configuration contract — snapshot + incremental jobs, restricted joins, mapping types and transforms — lives in [`config.example.yaml`](config.example.yaml).

# Container image

Multi-platform images (linux/amd64 + linux/arm64) are published to GHCR for every release tag:

```bash
docker run --rm ghcr.io/keveon/synclet --version
```

Run with a mounted config and env-based credentials (see `config.example.yaml` for the full contract):

```bash
docker run --rm \
  -v $PWD/config.yaml:/etc/synclet/config.yaml:ro \
  -v synclet-data:/var/lib/synclet \
  --env-file .env \
  ghcr.io/keveon/synclet --once
```

Notes:

- The container runs as a dedicated non-root user (uid 65532); bind-mounted checkpoint directories need `chown 65532:65532` on the host.
- Build locally from the repository root: `docker build -t synclet:dev .`

## Runtime paths

Deployed runtimes follow the Filesystem Hierarchy Standard:

| Purpose | Path |
| --- | --- |
| Binary | `/usr/local/bin/synclet` |
| Config | `/etc/synclet/config.yaml` |
| Checkpoint state | `/var/lib/synclet/state.json` |

## Roadmap

- [x] CLI, configuration contract, CI
- [x] Core sync engine: reader -> mapping -> writer, checkpoints, scheduling
- [x] First release: version tag and container image

## Principles

- **Losing a row is worse than duplicating it** — checkpoints advance only after successful writes.
- **Fail loudly rather than guess** — missing or ambiguous config is an error, never a silent default.
- **Trust no input** — identifiers validated, queries parameterized, secrets kept out of files.

## Development

```bash
gofmt -w ./cmd ./internal
go test ./...
go vet ./...
go build ./...
```

## License

[MIT](LICENSE) © keveon
