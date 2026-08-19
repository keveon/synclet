# synclet

[简体中文](README.zh-CN.md)

`synclet` is a lightweight, config-driven **DB polling -> DB upsert** sync tool:

```text
reader -> mapping -> writer
```

It polls a source database read-only, maps fields, and upserts into a target database. It manages no business schema of its own and requires no agent installed on the source.

- **PostgreSQL <-> MySQL, any direction**: use either database as source or target — PostgreSQL -> MySQL and MySQL -> PostgreSQL both work from the first release.

> **Status: skeleton repository.** The package layout and contracts are in place; the sync engine is not implemented yet — running returns an explicit not-implemented error instead of silently no-op'ing. The core implementation is the next step; see [Roadmap](#roadmap).

## Features

- **Credentials reference environment variable names only**: YAML contains just `connections.*.dsn_env` — never real DSNs, passwords or tokens.
- **Structured SQL config**: the reader builds parameterized queries from `table + alias + columns + joins + filters + cursor`; every identifier is validated — arbitrary SQL is rejected.
- **JOINs are a restricted capability**: `inner`/`left` equi-joins on `alias.column = alias.column` only; filter conditions reference the primary alias only.
- **Field mapping**: `column / literal / json_path / json_object / selector` types, with `required` (fail on missing, ignore `default`) and `default`; selectors try paths in order and take the first numeric value.
- **Ordered transforms**: `negative_to_zero`, `require_column_in`, `add_column`, executed in config order; decimal arithmetic goes through `decimal` — float values are rejected rather than masquerading as exact.
- **Two sync modes**: `snapshot` (full pull + upsert each round, for reference tables) and `incremental` (`cursor + tie_breaker` composite cursor, for fact tables).
- **No silent data loss**: the composite `(cursor, tie_breaker)` cursor prevents same-timestamp groups from being skipped; the checkpoint advances only after a successful write; JOINs apply after the fact batch is selected so `LIMIT` never truncates expanded rows.
- **Idempotent writer with stats**: upsert by key columns; `null_update_policy: keep_existing`; JSON merge patch columns; logs distinguish `attempted / inserted / updated / unchanged`.
- **Safe defaults**: fail-closed scope filter (an empty allowlist without an explicit `allow_all: true` is a config error); automatic redaction of DSNs / tokens / URL userinfo in logs; no checkpoint values or business payloads in logs.

## Repository layout

```text
cmd/synclet            CLI entrypoint
internal/              engine, reader, writer, mapping, checkpoint, ...
config.example.yaml    example configuration
```

Deployed runtimes follow the Filesystem Hierarchy Standard:

| Purpose | Path |
| --- | --- |
| Binary | `/usr/local/bin/synclet` |
| Config | `/etc/synclet/config.yaml` |
| Checkpoint state | `/var/lib/synclet/state.json` |

## Quick start

Prerequisites: Go 1.26+.

```bash
git clone https://github.com/keveon/synclet.git
cd synclet

cp config.example.yaml config.yaml
# For a local trial, point checkpoint.path at a writable directory.

export SOURCE_DSN='<PostgreSQL DSN>'
export TARGET_DSN='<MySQL DSN>'

# single pass
go run ./cmd/synclet --config config.yaml --once

# loop mode (default)
go run ./cmd/synclet --config config.yaml
```

The CLI accepts long options only (`--config`, `--once`, `--version`, `--help`); single-dash forms are rejected.

See [`config.example.yaml`](config.example.yaml) for the full config contract — snapshot + incremental jobs, restricted JOINs, mapping types and transforms.

## Roadmap

- [x] Skeleton: package layout, contracts, CLI, CI
- [ ] Implement the core sync engine: `internal/engine` orchestrating reader -> mapping -> writer, with checkpoint, config, mapping, redaction and logging
- [ ] Implement readers and writers for both PostgreSQL and MySQL, so any PostgreSQL/MySQL pairing syncs in either direction from the first release
- [ ] First release: version tag and container image

## Design principles

- **Fail closed over silent degradation**: missing authoritative fields, unknown kinds or incomplete configs are errors — never papered over with defaults.
- **Losing rows is worse than duplicating them**: the checkpoint advances only after a successful write; same-timestamp groups are disambiguated by the composite cursor.
- **Don't trust input**: no arbitrary SQL, identifiers validated, credentials never in YAML.
- **Observable**: structured event logs (`start`/`read`/`complete`), reconcilable write stats, redacted error output.

## Development

```bash
gofmt -w ./cmd ./internal
go test ./...
go vet ./...
go build ./...
```

## License

[MIT](LICENSE) © keveon
