# Contributing to SGOtel

Thanks for your interest in contributing. Here's what you need to know.

## Getting started

```sh
git clone https://github.com/tight-line/sgotel.git
cd sgotel
make build
make test
```

The test suite is unit + handler-integration only; no live SendGrid or OTel
collector is required.

## Submitting changes

1. Fork the repo and create a branch from `main`.
2. Make your changes and add tests where appropriate.
3. Ensure `go vet ./...` and `go test -race ./...` both pass.
4. Open a pull request against `main` with a clear description of what changed
   and why.

For non-trivial changes, open an issue first to discuss the approach.

## Coding conventions

- Standard Go formatting (`gofmt`). No linter configs are committed; follow
  what's already there.
- Keep cardinality out of metric labels (no email addresses, no `sg_message_id`,
  no `sg_event_id`).
- Configuration via environment variables only; no new config file formats.

## Reporting bugs

Open a GitHub issue with enough detail to reproduce the problem: SGOtel version,
Go version, relevant env vars (redact secrets), and a description of observed vs.
expected behavior.

## Security issues

Please do **not** open a public issue for security vulnerabilities. See
[SECURITY.md](SECURITY.md) for how to report them privately.

## License

By contributing you agree that your contributions will be licensed under the
[MIT License](LICENSE).
