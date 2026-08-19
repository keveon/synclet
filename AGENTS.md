# AGENTS.md

This file is for AI coding agents (and future contributors). It describes the repository layout, conventions and red lines.

## Repository status

Core engine implemented. The reader -> mapping -> writer pipeline, checkpoints, loop scheduling and the CLI are functional:

- `cmd/synclet/main.go`: CLI entrypoint — strict long-option parsing (`--config`, `--once`, `--version`, `--help`; single-dash forms are rejected, `--help` exits 0).
- `internal/*`: per-package implementations (see the package map below); each package's `doc.go` is the authoritative contract description.
- `config.example.yaml`: the full config contract (two jobs: snapshot `customers` + incremental `orders`).

## Package map

```text
cmd/synclet          CLI entrypoint: option parsing, wiring, version
internal/engine      sync engine: orchestrates reader -> mapping -> writer
internal/reader      read-only source polling, PostgreSQL + MySQL
internal/mapping     field mapping (column/literal/json_path/json_object/selector) + transforms
internal/writer      idempotent target writes, PostgreSQL + MySQL
internal/checkpoint  incremental cursor persistence (atomic writes, advances only after success)
internal/config      YAML config loading and validation (fail-closed)
internal/filter      scope allowlist resolution (values_from: scope.allowed_codes)
internal/jsonpath    restricted dot-path evaluation
internal/redact      log/error redaction
internal/logging     structured event logging
internal/dbutil      identifier validation, SQL and DSN helpers
internal/model       shared core types
config.example.yaml  example configuration (deployed to /etc/synclet/config.yaml)
```

The authoritative description of each package is its `doc.go`.

## FHS runtime paths

The repository itself is a plain Go project (example config at the root). FHS applies to deployed runtimes:

- Config: `/etc/synclet/config.yaml`
- Runtime state (checkpoint): `/var/lib/synclet/`
- Binary: `/usr/local/bin/synclet`

## Hard rules

1. **Credentials red line**: no real DSN, password or token may appear in any config, code, test, log or commit message. YAML references `dsn_env` names only; tests use RFC 5737 documentation addresses, redacted passwords and placeholder codes (C001/C002).
2. **Commit message red line**: never mention client, company or project-specific names; use generic descriptions.
3. **Fail closed**: missing fields, unknown kinds or incomplete configs are errors — never papered over with defaults. An empty `scope` without an explicit `allow_all: true` fails validation.
4. **No data loss**: the checkpoint advances only after a successful write; increments use the composite `(cursor, tie_breaker)` cursor.
5. **No arbitrary SQL**: readers are built from structured config with identifier validation and parameterized queries only.
6. **Commits and public text**: Conventional Commits (`feat:` / `fix:` / `docs:` / `chore:` / `refactor:`), signed with `-S`. Commit messages, PRs and issues are written in English. `README.md` is English; `README.zh-CN.md` is the Chinese translation — keep the language-switch links symmetric in both files. Never expose real names; the copyright holder is `keveon`.
7. **CLI conventions**: long options only (double dash); `--help` and `--version` exit 0. Describe upcoming work as "implementing" it — the code here is written fresh for this repository.

## Verification

Every change must pass before merging:

```bash
gofmt -l ./cmd ./internal        # must print nothing
go test ./...
go vet ./...
go build ./...
git diff --check
```

CI (`.github/workflows/ci.yml`) runs the same checks on push/PR.

## Workflow

- Default branch `main`; feature work flows through feature branches + PRs.
- PR titles follow Conventional Commits, same as commits.
