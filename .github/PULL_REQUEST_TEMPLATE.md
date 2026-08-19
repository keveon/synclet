## Changes

<!-- What does this PR do, and why -->

## Verification

- [ ] `gofmt -l ./cmd ./internal` prints nothing
- [ ] `go test ./...` passes
- [ ] `go vet ./...` passes
- [ ] `go build ./...` passes

## Conventions

- [ ] Title and commits follow Conventional Commits (`feat:` / `fix:` / `docs:` / `chore:` / `refactor:`)

## Red-line self-check

- [ ] No real DSNs, passwords or tokens (config references `dsn_env` names only)
- [ ] No client, company or project-specific names
