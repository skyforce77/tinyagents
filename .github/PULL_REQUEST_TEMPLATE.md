## What does this change?

<!-- One or two sentences on the user-visible change or refactor. Link related issues with "Fixes #123" if any. -->

## Why?

<!-- Motivation: user need, upstream bug, architectural gap, etc. -->

## How to test?

<!-- Commands reviewers can run to verify, e.g. `go test -race ./pkg/agent/...` or a CLI invocation. -->

## Checklist

- [ ] `go vet ./...` is clean
- [ ] `go test -race -count=1 ./...` passes
- [ ] New exported symbols have godoc
- [ ] Commit subject is one of the `MX.Y: …` deliverable conventions (or a `chore:` / `docs:` prefix) and is ≤ 72 characters
- [ ] No `Co-Authored-By:` trailer
- [ ] PR targets `master`
