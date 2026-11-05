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
