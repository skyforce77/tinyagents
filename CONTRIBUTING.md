# Contributing to tinyagents

Issues and pull requests are welcome. Please read this guide before opening a PR.

---

## Dev setup

```bash
git clone https://github.com/skyforce77/tinyagents.git
cd tinyagents
go mod download
```

Go 1.24 or later is required. No additional build tools are needed for the core
library; examples with external services (Ollama, Anthropic, OpenAI) require the
respective credentials and running services.

---

## Run tests

```bash
go test -race ./...
go vet ./...
```

CI runs both commands on every push and pull request. A PR cannot merge while
either command reports failures. If you add a new package, add tests in the same
commit — a package without tests will block the CI race check.

`golangci-lint run` is also expected to pass. Install it with:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

---

## Commit conventions

- One commit per roadmap deliverable. Commit subjects follow the pattern used in
  the existing history: `M0.X:`, `M1.X:`, `M2.X:`, `M3.X:`, `M4.X:`, `M5.X:`.
  If your change does not map to a milestone, use a plain imperative subject.
- Subject line: 72 characters maximum, imperative mood ("Add…", "Fix…", "Remove…").
- Body lines: wrap at 72 characters. Explain *why*, not just *what*.
- Never include a `Co-Authored-By:` trailer.

```
M3.2: add Debate team coordinator

Two debater agents exchange N rounds of arguments and then an arbiter
agent produces a final verdict. All three speak Prompt/Response, so
Debate nests inside any other team coordinator.
```

---

## Pull requests

- Target `master`. Rebase onto `master` before opening the PR; do not merge
  `master` into your branch (no merge commits).
- Link the related issue in the PR description when one exists.
- Each PR should do one thing. Refactoring, feature work, and test fixes belong
  in separate PRs unless they are inseparable.
- Keep the diff reviewable: prefer smaller, focused commits over one large squash.

---

## Code style

- `go vet ./...` and `golangci-lint run` must both pass.
- Prefer interfaces over concrete types at package boundaries.
- Every exported symbol must have a godoc comment. Match the voice and style of
  `pkg/supervisor/restart.go` or `pkg/mailbox/mailbox.go`: short first sentence
  ending with a period, expand with additional paragraphs when needed.
- Avoid abbreviations in exported names (`maxRestarts` is fine in a struct field;
  `mr` is not). Single-letter variables are acceptable inside short closures.
- Do not add `//nolint` directives without a comment explaining the exception.

---

## Testing

- Every new package needs at least one `_test.go` file that exercises the public
  API under the race detector (`go test -race`).
- Use `net/http/httptest` for adapters that call external HTTP APIs; do not make
  real network calls in tests.
- Do not use `time.Sleep` as a synchronization mechanism. Poll with a deadline
  (`context.WithTimeout`) or use channels instead.
- Table-driven tests are preferred for coverage of multiple input shapes.

---

## Reporting issues

When opening a bug report, please include:

1. Go version (`go version`)
2. OS and architecture
3. A minimal, self-contained reproduction (ideally a single `main.go` or a test)
4. The full error output and any relevant log lines
5. Expected behaviour vs. actual behaviour

Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md).

---

## License

By contributing to tinyagents, you agree that your changes will be licensed
under the [GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0), the
same license that governs the rest of the project.
