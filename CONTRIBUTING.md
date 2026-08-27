# Contributing

## Development Setup

- Install Go `1.27` or newer
- Clone the repository
- Run `go test ./...` before opening a pull request

Optional local task shortcuts:

```bash
 mise run build
 mise run test
 mise run install
```

## Pull Requests

- Keep changes focused and easy to review
- Add or update tests when behavior changes
- Update `README.md` and CLI help text when install or usage changes
- Verify `go test ./...` passes before submitting
