# Woodpecker upgrade modules

Owner: WPCK#15. Program: WFO#1016. Source: `wpck-v3.19.0` at `3f6a236dba3981889103fe11a0b30e7522839120`.

## Model

Use a clean upstream tag as a new release base. Apply the four exact SHA256-pinned patches in `modules.json` order; check conflicts and whether upstream already absorbed the change. Do not merge the entire prior version branch. Modules are independent *intentions*, but `github.go` is shared between the first three patches: patch ordering and exact semantic tests are mandatory.

1. `github-branch-cache` - reuse successful branch GET results, coalesce calls and respect 403/429. Existing WPCK#1.
2. `github-status-route` - opt-in outbound status identity. Keep disabled; never enable `only50bw` without separately approved compliance and credential gates. Existing WPCK#5.
3. `github-http-metrics` - real outbound GitHub HTTP counters. Existing WPCK#9.
4. `source-ci` - source-only pipeline and nonprivileged checks. Existing WPCK#13; move upstream vendor workflows out of active discovery without deleting their content.

## Additional work outside the four code patches

- Queue/redispatch corrections WFO#770 and WFO#773 are archived separately in WFO and not in the WPCK v3.19.0 branch; compare actual v3.20.1 code before deciding to port. **Do not claim existing binary contains them.**
- Woodpecker custom CSS/JS and agent capacity are external runtime assets/configuration. SRV#863 owns their inventory, version-compatibility tests and rollback. Never copy secrets into this public repo.
- Upstream v3.20.0 explicitly warns of unintended breaking changes; target v3.20.1 or newer after independent security/release review.

## Repeatable upgrade gates

- Pin official tag *and immutable commit SHA*; verify release notes, provenance, source checksums.
- Check for upstream-absorbed changes; skip only with evidence and contract tests.
- Apply each patch independently in the declared order, never force-push or silently discard conflicts.
- Run SHA256 integrity, `git diff --check`, formatting, Go unit/race/vet, complete server test with baseline delta, authentic web build, isolated server/agent/SQLite tests, GitHub status delivery and metrics.
- Source PR merge is not runtime deployment. Live release requires SRV#863 / WFO#1016 protected approval and proof of recovery.
- Retain exact previous source commit, binary checksums, DB/WAL backup consistency, asset checksums and rollback. Do not interrupt SRV#837 observation.

## 3.20.1 candidate: five modules and verified upgrade operator (WFO#1016)

This historical manifest started with four GitHub/source modules from the 3.19.0 fork.
For the 3.20.1 candidate, WPCK#21 independently ported bounded SQLite lease recovery,
restart persistence, safe re-dispatch and reset of prior successful steps. The fifth
`queue-lease-recovery` patch is SHA256-pinned in the versioned `modules.json`.
Apply order is GitHub cache -> status route (OFF by default) -> HTTP metrics ->
source CI -> queue lease recovery. Never assert an upstream change was absorbed
without a failing-before and passing-after contract test.

The source-only operator `maintenance/upgrade/scripts/upgrade.py` (from WPCK#17)
now loads all five modules, verifies hashes, pins official upstream SHA and
generates isolated reviewed worktrees. `verify/plan/apply` never pushes, deploys,
changes live settings, reads secrets or modifies services. `apply` deliberately
returns a conflict requiring human/code review for upstream drift; **a new version
is not automatically safe to publish**. Source tests include Python compilation,
7 regression tests, failure on tool-output contamination, GitHub/queue Go tests,
and authentic frontend + server/agent build before release. Live release still
requires SRV#863 protected gates (CGO1/SQLite, real DB backup/restore, CSS/JS,
post-release metrics and owner approval).
