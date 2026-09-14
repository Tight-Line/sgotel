---
name: apply-security-upgrades
description: Clear every outstanding advisory in one branch and get govulncheck, Snyk, SonarCloud, lint and the coverage gate green. Use periodically to clear the dependency backlog, or when Dependabot PRs are piling up red.
---

# Apply security upgrades

Collapse the open Dependabot PRs and whatever no dependency PR can fix into a
single reviewable branch, then get the security and quality gates green. One
branch, one PR, one CI run instead of five that each burn a full matrix.

Adapted from the same skill in `ballast`. The differences are load-bearing and
are called out where they matter; do not carry ballast's habits over blind.

## Why a single branch

Dependabot opens one PR per ecosystem group. Each runs the whole CI matrix, each
has to be merged and then rebased against the others, and none of them can fix a
problem that lives outside its own diff, such as a stale `go` directive. Merging
them together first means one CI run, one review, and a place to put the fixes
that no individual bump could carry.

## Procedure

### 1. Survey

```bash
gh pr list --limit 50 --json number,title,headRefName,author \
  --jq '.[] | select(.author.login=="app/dependabot") | "\(.number)\t\(.headRefName)\t\(.title)"'
```

An empty list is not good news. It may mean Dependabot is switched off, which is
the state sgotel was in through v0.0.3. Check before you conclude anything:

```bash
test -f .github/dependabot.yml && echo "configured" || echo "NOT CONFIGURED"
gh api repos/Tight-Line/sgotel --jq '.security_and_analysis'
gh api repos/Tight-Line/sgotel/dependabot/alerts --paginate \
  --jq '.[] | select(.state=="open") | "\(.security_advisory.severity)\t\(.dependency.package.name)\t\(.security_advisory.ghsa_id)"'
```

**The scanners are not the source of truth. `govulncheck` is.** It is the only
one of the three that knows which vulnerable code SGOtel actually calls, and it
is the only one that reports on the standard library. Run it first:

```bash
make vulncheck    # pinned govulncheck via `go run`; see the Makefile
```

Then capture the gate status for the PRs and **for `main` too**:

```bash
gh pr checks <n>
gh run list --branch main --limit 15 \
  --json conclusion,name,headSha,createdAt \
  --jq '.[] | "\(.conclusion)\t\(.name)\t\(.headSha[0:8])"'
```

> If a check is red on *every* PR including ones that touch only the Dockerfile,
> it is not the dependency bumps. It is infrastructure, and it is almost
> certainly also red on `main`. Diagnose it before touching any dependency.

### 2. Build the branch

```bash
git checkout -b chore/security-upgrades-<YYYY-MM> origin/main
```

**Merge, do not cherry-pick.** This is the opposite of ballast. sgotel's `main`
carries merge commits (`Merge pull request #13 from ...`) and the repo allows
all three merge methods, so there is no rebase replay to trip over and no reason
to rewrite Dependabot's commits. `main` is also not branch-protected here, which
means nothing stops a direct push; use a PR anyway, since the PR is where Snyk
and Sonar run with real tokens.

Apply in a deliberate order, cheapest and least conflict-prone first, Go modules
last, and within Go modules the **grouped minor/patch bump before the security
bump** so the security pin is the one that survives:

1. `github-actions` group
2. `docker` bumps
3. `gomod` minor/patch group
4. `gomod` security group

### 3. Resolve go.mod / go.sum conflicts

Do **not** hand-merge the version lines. Take the already-applied side, then
re-apply the incoming pin through the toolchain so `go.sum` stays internally
consistent:

```bash
git checkout --ours go.mod go.sum
go get <module>@<version>   # re-apply each security pin the incoming commit carried
go mod tidy
git add go.mod go.sum
```

Rule for each conflicting line: **take the higher version**. The minor/patch
group is often newer for transitive deps such as `genproto` and `protobuf`,
while the security group is newer for the one module it targets. Both need to
win where they are ahead.

`go mod tidy` may legitimately prune `go.sum` lines for the version being
replaced. That is correct, not drift, but it means the tree is no longer
byte-identical to a previously verified one, so re-run the gate.

### 4. Fix the gates

```bash
make check       # lint + test-coverage-check + build
make vulncheck   # govulncheck, deliberately not part of check
```

`vulncheck` is kept out of `check` on purpose: `scripts/make-tag` gates on
`check`, and a release should not fail on a `vuln.go.dev` outage.

Known classes of problem, in the order they tend to bite:

#### The stale `go` directive (Dependabot cannot fix this)

`govulncheck` reports Go **standard library** vulnerabilities, and Dependabot
never touches the `go` directive. So the stdlib quietly rots and `govulncheck`
fails with findings that no dependency PR can clear. Seven of them had
accumulated by v0.0.3.

**Correct the mental model here; ballast's copy of this skill states it
loosely.** `govulncheck` reports against the toolchain the build actually
resolves to, not against the text of the `go` directive. The directive is a
floor: if go.mod asks for less than the toolchain you have installed, the build
and the scan both use the installed one, and the findings come back against that
rather than against the directive. Raising the directive helps because it raises
that floor for everyone. Verify what you are actually scanning:

```bash
go env GOVERSION     # what the directive resolved to
```

Bump the directive to the **latest patch on the module's current minor line**. A
minor bump changes language semantics and vet/lint behavior and does not belong
in a security pass **unless a dependency forces it or the owner asks for it**,
both of which happened in the 2026-09 pass: `golang.org/x/net` and
`golang.org/x/text` required `go >= 1.26.0` from the versions carrying their
fixes, putting 1.25 out of reach, and the directive then went to **1.27.1** on
the owner's call. SGOtel is on the **1.27.x** line; do not propose 1.26.
Say in the commit message which of the two reasons applies.

A minor bump drags the build tooling with it, so budget for that rather than
treating it as a surprise. Go 1.27 broke two things here: golangci-lint refuses
to start when the Go that built it is older than the version it targets, and
coverage attribution moved, which is why `scripts/check-coverage.sh` looks two
lines above an uncovered line for `coverage:ignore` rather than one.

```bash
# what patch releases exist
curl -s "https://go.dev/dl/?mode=json&include=all" \
  | grep -oE '"version": "go1\.[0-9]+\.[0-9]+"' | sort -u -V | tail
# setup-go must be able to install it
curl -s "https://raw.githubusercontent.com/actions/go-versions/main/versions-manifest.json" \
  | grep -oE '"version": "1\.[0-9]+\.[0-9]+"' | sort -u -V | tail

go mod edit -go=<version>    # does not add a `toolchain` directive; keep it that way
make vulncheck               # must print "No vulnerabilities found."
```

Check the advisory's own fixed ranges before assuming a minor bump is required.
This settles it in one call per ID:

```bash
curl -s "https://vuln.go.dev/ID/GO-2026-6218.json" | tr ',' '\n' | grep -E '"fixed"|"introduced"'
```

#### Version drift across go.mod, the workflows and the Dockerfile

This repo used to write the Go version in three places: the `go` directive,
`go-version: '1.25'` at five workflow sites, and `golang:1.25-alpine` in the
Dockerfile. Three places to bump means in practice none of them move.

The workflows now read `go-version-file: go.mod`, so the directive is the only
place a version is written down and CI gets the exact patch instead of whatever
the runner manifest calls that minor line this week. **Keep it that way.** If you bump the
directive, the only other file to touch is the Dockerfile builder stage. The
builder image and the directive do not strictly need to match (a newer toolchain
building an older-directive module is fine), but keeping them equal removes a
question nobody wants to re-answer.

#### Do not put build tooling in go.mod

Sonar's `githubactions:S8545` asks for lock-file-enforced tool versions, and Go's
answer is a `tool` directive. **Do not take that bait here.** A tool directive
puts the tool's whole dependency tree in this module's graph, and Snyk scans that
graph as if it shipped in the binary.

Measured in the 2026-09 pass: golangci-lint as a tool directive took the module
graph from 95 to 452. govulncheck looked cheap at 7 modules, went in on that
basis, and still failed the Snyk gate, because it pulls `golang.org/x/tools`,
which pulls `goldmark`, which had an open XSS advisory. SGOtel has never
contained a markdown renderer.

Both tools are pinned in the Makefile and invoked with `go run <pkg>@<version>`
instead. That is reproducible, keeps local and CI identical, builds the tool with
the toolchain the `go` directive selects, and leaves go.mod at 95 modules. S8545
stays open against any remaining `go install` line in a workflow; that is the
accepted trade, not an oversight.

#### Snyk or Sonar red for reasons that are not findings

Check *why* the job is red before hunting for vulnerabilities. A scanner that
dies in setup reports the same red X as one that found a critical CVE.

As of the 2026-09 pass, **`Snyk Security` has been failing on `main` for weeks
with `Authentication error (SNYK-0005)` / `401 Unauthorized`**. The `SNYK_TOKEN`
repository secret is expired or its user is not provisioned. No PR can fix that;
it needs the secret rotated in repo settings. The job then also fails at
`upload-sarif` with `Path does not exist: snyk.sarif`, which is a symptom, not
the cause.

```bash
RUN=$(gh run list --workflow=snyk.yml --branch main --limit 1 --json databaseId --jq '.[0].databaseId')
gh run view "$RUN" --json jobs \
  --jq '.jobs[] | {name, conclusion, failed: [.steps[]|select(.conclusion=="failure")|.name]}'
JOB=$(gh run view "$RUN" --json jobs --jq '.jobs[0].databaseId')
gh run view --job "$JOB" --log | grep -iE "ERROR|Unauthorized|Tested .* dependencies|issues found" | cut -c1-300
```

Note that on Dependabot and fork runs `SONAR_TOKEN` and `SNYK_TOKEN` are empty,
so both jobs skip and report green. **A green Snyk or Sonar check on a Dependabot
PR means nothing was scanned.** They run for real on this branch, because it is
pushed from the repo rather than by Dependabot. Verify a scanner actually
scanned before calling it green: a real Snyk run prints
`Tested N dependencies for known issues`, a real Sonar run prints
`ANALYSIS SUCCESSFUL` and `EXECUTION SUCCESS`.

#### Dependency bumps that need a code change

Most bumps are a version line. The OpenTelemetry **logs** packages are the
exception in this repo: `go.opentelemetry.io/otel/log` and the `otlplog`
exporters are still **v0.x** and are allowed to break their API, and they do. In
v0.22.0 the package deleted its own `Value` and `KeyValue` types and reused
`go.opentelemetry.io/otel/attribute` instead, so `Record.SetBody`,
`AddAttributes` and `WalkAttributes` all changed signature.

When `go get -u ./...` produces `undefined: otellog.<something>`, that is what
happened. Diff the API rather than guessing:

```bash
go doc -all go.opentelemetry.io/otel/log | grep -E "^(func|type) "
```

Keep this kind of migration in **its own commit**, separate from the security
bumps, so a reviewer can drop it without losing the advisory fixes. It is not
security work and should be described as routine under `### Changed`.

#### Coverage gate

`scripts/check-coverage.sh` fails on any uncovered line without a
`// coverage:ignore - <reason>` comment, rather than on a percentage. A
dependency bump rarely moves it, but a refactor forced by a broken API can. It
reproduces locally and exactly: `make test-coverage-check`.

### 5. Changelog and commit

**A security pass must leave a `### Security` entry under `## [Unreleased]`.**
This is the one thing that cannot be skipped. `scripts/make-tag` refuses to cut a
release when `[Unreleased]` is empty, so with no entry there is no way to ship
the patched build at all, and the fix ends up riding along with whatever feature
lands next. The entry is also the only way a user learns why the patch release is
worth taking, since by definition they cannot see the change.

This is an explicit carve-out from the usual "user-visible changes only" rule;
see the Changelog section in `CONTRIBUTING.md`. Write the advisory IDs and what
they affect, not just "bumped dependencies". Routine bumps carrying no security
fix do not each need a line; summarize those under `### Changed`.

Confirm `make-tag` will actually accept what you wrote, rather than assuming:

```bash
UNRELEASED=$(sed -n '/^## \[Unreleased\]/,/^## \[/p' CHANGELOG.md | sed '1d;$d')
[ -z "$(echo "$UNRELEASED" | grep -v '^$' | grep -v '^###')" ] && echo "make-tag WILL FAIL" || echo ok
```

Do not touch version numbers or tags. Releases go through `scripts/make-tag`,
which also bumps `charts/sgotel/Chart.yaml`.

Commit with an explicit pathspec. The working tree usually has an untracked
local `sgotel.yaml`, and `git commit -a` will not help you:

```bash
git status
git commit -m "..." -- <paths>
make check     # must be green on the committed tree
```

### 6. Push and verify

```bash
git push -u origin chore/security-upgrades-<YYYY-MM>
gh pr create --fill
gh pr checks --watch
```

This branch exists specifically to fix red gates, so a green local run is not the
deliverable. Watch them go green.

A dependency-heavy failure right after resolving a `go.sum` conflict is worth one
check before writing it off as a network transient. "all modules verified" means
the checksums are sound and the failure was the network:

```bash
go mod download && echo ok
go mod verify
```

Re-runs only work once the whole run has finished; while sibling jobs are still
going it refuses with "This workflow is already running":

```bash
gh run rerun <run_id> --failed
```

## Checklist

- [ ] Every open Dependabot PR either merged into the branch or explicitly noted as superseded
- [ ] `go.mod` and `go.sum` conflicts resolved to the higher version on both sides
- [ ] `go` directive at the latest patch of its minor line, and a minor-line move justified in the commit message
- [ ] The Dockerfile builder stage matches the directive; no `go-version:` re-introduced into a workflow
- [ ] `make vulncheck` prints "No vulnerabilities found."
- [ ] `make check` green on the committed tree
- [ ] Each red gate diagnosed from its real log, not assumed to be a finding
- [ ] Any breaking-API migration is its own commit
- [ ] `CHANGELOG.md` `[Unreleased]` has a `### Security` entry naming the advisories, and the `make-tag` check above passes
- [ ] PR checks watched to completion
