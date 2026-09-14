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

## Changelog

`CHANGELOG.md` keeps an `[Unreleased]` section that accumulates entries until a
release. `scripts/make-tag` moves them into a dated section and creates the tag.

Entries are for changes a user would want to know about before upgrading. That
is usually visible behavior, but it also covers security work that changes
nothing a user can see: a dependency bump carrying a CVE fix, a `go` directive
bump clearing a standard library advisory, or a fix to the build pipeline
itself. Those go under `### Security`.

Including them is deliberate rather than a loosening of the rule.
`scripts/make-tag` refuses to cut a release when `[Unreleased]` is empty, so a
security-only release is impossible without an entry; the alternative is
shipping the fix silently attached to whatever feature happens to land next.
The entry is also the only place a user finds out why a patch release is worth
taking, since by definition they cannot see the change themselves.

Write the advisory IDs and what they affect, not just "bumped dependencies".

Routine dependency bumps that carry no security fix do not each need an entry.
Summarize them in a single line under `### Changed` when a release is being cut
anyway; on their own they are not a reason to cut one.

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
