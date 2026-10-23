# Repository notes

Working notes for `db-auth-gateway`. The aim is to record things that are not
obvious from the code or the git history — conventions, review mechanics, and
the handful of traps that cost someone an afternoon. Add to it as you find more.

## Build, test, lint

`make help` lists every target. The ones CI runs:

| Target | What it covers |
| --- | --- |
| `make mod-check` | `go.mod` / `go.sum` are tidy |
| `make test` | unit tests with coverage |
| `make lint` | `golangci-lint`, pinned and fetched via `go run` |
| `make build` | binary into `bin/` |
| `make test-e2e` | builds Docker images, runs e2e against real containers |

`make check` (`fmt` + `lint` + `test`) is the quick pre-push pass. `make
install-hooks` wires up the `pre-commit` hooks (`gofmt`, `go vet`,
`golangci-lint`).

Two checks fail on generated output drifting rather than on bad code:

- `make check-license` — every Go file needs its SPDX header.
- `make check-reference-help` — regenerate with `make reference-help` after
  changing any CLI flag, or `build` fails.

## Review and merge mechanics

`main` is governed by a repository **ruleset**, not classic branch protection,
so `gh api repos/grafana/db-auth-gateway/branches/main/protection` returns 404.
Read it with:

```sh
gh api repos/grafana/db-auth-gateway/rules/branches/main
```

What it enforces:

- 1 approving review. `require_code_owner_review` is **false**, so the
  `CODEOWNERS` requests from `mimir-maintainers` and `loki-team` are advisory —
  any single approval from someone with write access satisfies the rule, and a
  surplus team can be un-requested.
- `dismiss_stale_reviews_on_push` — any Renovate rebase drops existing
  approvals, so a conflicted dependency PR needs re-approving after it rebases.
- `require_last_push_approval` — the approver cannot be the last pusher.
- Squash merge only. `merge` and `rebase` are disabled at the repo level.

Un-requesting a team needs both keys in the payload, or the API 422s:

```sh
echo '{"reviewers":[],"team_reviewers":["mimir-maintainers"]}' \
  | gh api -X DELETE repos/grafana/db-auth-gateway/pulls/<n>/requested_reviewers --input -
```

Merging a PR that touches `.github/workflows/**` needs a token with the
`workflow` scope (`gh auth refresh -h github.com -s workflow`); without it the
merge is refused as a base-branch policy violation.

## Renovate

Config lives in `.github/renovate.json5`. Grafana's self-hosted Renovate
already applies the shared presets org-wide; this repo only opts into
`automerge` and adds a few local rules.

Consequences worth knowing:

- **Approving a dependency PR usually merges it.** The `automerge` preset
  auto-merges patch, pin and digest updates once CI passes, so an approval is
  effectively the merge trigger for that class.
- **Indirect Go dependencies do get ordinary updates here.** The shared
  `go-only-security-for-indirect` preset is *not* applied, so indirect modules
  show up as normal `chore(deps)` PRs, not only as security fixes.
- **Security PRs cannot be grouped from this repo.** The self-hosted config
  pins the whole `vulnerabilityAlerts` object inside `force{}` with
  `groupName: 'security {{{packageFileDir}}} {{{depName}}}'` — grouped per
  dependency. `force` is `globalOnly` and overrides repo config outright, so
  nothing in `.github/renovate.json5` will change it. Grouping security updates
  differently means changing
  `deployment_tools/ksonnet/lib/renovate-self-hosted/renovate-base.libsonnet`,
  which affects every Grafana repo.
- Validate config changes with the official validator in Docker, never `npx`:

  ```sh
  docker run --rm -v "$PWD:/work" -w /work \
    --entrypoint renovate-config-validator renovate/renovate .github/renovate.json5
  ```

  It only checks the schema. It will happily pass a `matchPackageNames` pattern
  that matches nothing, so test the pattern against real module names too.

## Dependency traps

Recurring failures that are not the PR's fault:

- **`golang.org/x/net` bumps fail `lint`.** Newer `x/net` marks
  `http2.Transport`, `http2.ConfigureTransport` and `Transport.RoundTrip` as
  deprecated, and `staticcheck` SA1019 fails the build on three uses in
  `pkg/util/proxy/http.go`. Until that file moves to
  `http.Transport.Protocols`, every PR pulling a newer `x/net` goes red on lint
  alone.
- **The `go.etcd.io/etcd` modules must move together.** Bumping `api/v3` while
  `client/v3` lags breaks the interface —
  `*retryKVClient does not implement etcdserverpb.KVClient (missing method RangeStream)`.
- **The OpenTelemetry modules must move together.** v1.x stable and v0.x
  log-signal modules ship from the same release (v1.45.0 pairs with v0.21.0);
  split across PRs, `sdk/log` lands against an older `log` and fails with
  `undefined: log.KeyValue`. A `packageRules` group keeps the normal update
  path consistent — but see the security-PR caveat above.
- **`github.com/armon/go-metrics` is renamed.** It now declares its path as
  `github.com/hashicorp/go-metrics`, so any update to the old path fails
  `mod-check` with `module declares its path as ...`. Updates to that module
  path are not mergeable as-is.

## Commits and pull requests

- Conventional Commits for every subject line: `type(scope): Description`,
  imperative mood, no trailing full stop. It lands in the changelog.
- Commit bodies describe *what* changed and the technical *why*. Operational
  backstory (incidents, hotpatches, cluster drift) belongs in the PR
  description, not `git log`.
- Commits must be signed — `required_signatures` is on in the ruleset.
