---
name: architecture-review
description: Bounded-context boundary and ADR-compliance review of a change (expensive, post-integration): hexagonal direction, cross-context coupling, contradicted ADRs. Invoke explicitly: /architecture-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a bounded-context boundary and ADR-compliance review of the
current changes (or `$ARGUMENTS` if given, e.g. a branch/PR diff range).

This is EXPENSIVE relative to `/code-review` — it reasons about
cross-repo/cross-context implications, not just this diff's local
correctness. Use it post-integration (before merging a PR that touches
architecture, not on every small commit) or when asked explicitly to
check a design decision against this fleet's standing architecture.

## What to check, in priority order

1. **Hexagonal dependency direction.** Domain depends on nothing;
   application depends on domain+ports; adapters depend on
   application+domain; only `cmd/` wires every layer together. Run
   `go test ./internal/architecture/... -v` first — if it's already red,
   report that and stop; don't hand-review what a fitness test already
   caught.
2. **Customer/Supplier direction, per `.claude/rules/domain-model.md`
   (or this repo's equivalent doc).** A new outbound call to a sibling
   context must go the direction ADRs already established — check
   `docs/adr/` (ADR 0001 is this repo's context map: material-flow-control
   upstream caller, the PLC Conformist, exception-management downstream) before assuming
   a new integration is fine. Flag any outbound call added to a context
   this repo doesn't already integrate with; that's a new architectural
   decision that needs its own ADR, not a code change slipped in
   silently.
3. **MCP additive-boundary rule (fleet-wide, `.claude/rules/fleet/no-auth-and-mcp.md`).** This repo has no
   MCP adapter yet. When one is added under internal/adapters/inbound/mcp/ it must depend only on
   application/domain, and nothing else in the codebase may depend on
   it; the MCP authority fitness tests in `internal/architecture/` then stop being vacuous.
2b. **Zero-write guardrails, where applicable.** If this repo has a
   documented zero-write constraint (e.g. warehouse-ops-agent v1), check
   no mutating HTTP method or MCP tool without `ReadOnlyHint: true` was
   added to an outbound/inbound surface bound by that constraint.
4. **Invariant 6: nothing inside the rack leaves the context.** No rack position
   (`layout.Position`), reshuffle job or internal move may appear in a REST body
   (`internal/adapters/inbound/http/dto.go`), an event payload
   (`internal/adapters/outbound/events/encode.go`) or `apis/*.yaml` outside the MQTT
   telegram channels. A change that exposes one is a NEEDS-ADR finding (seam decision 2 of the
   WCS design). There is no analytics read side in this repo; the `.analytics` topic only
   carries the twin of every event.
5. **The PLC link and the single scheduler (ADR 0002).** Down telegrams leave only from
   a `UnitOfWork.AfterCommit` hook; an up telegram is claimed in `processed_telegrams` in the
   same transaction as its effect; every write locks its aisle (`usecases.Service.inAisle`);
   one job per aisle unit is with the PLC. A change that sends from inside a transaction, skips
   the claim, writes outside an aisle unit of work or allows a second replica
   (`charts/asrs-control` refuses `replicaCount` other than 1) contradicts ADR 0002. This repo
   has no Kafka consumer; a first one needs an ADR and the consumer-group rules in
   `.claude/skills/how-to-add-an-integration-event/SKILL.md`.
6. **A new bounded-context integration with no companion documentation.**
   If this change introduces or changes a cross-repo contract (a new
   REST call, a new Kafka topic subscription, a new MCP tool consumed by
   a sibling), check whether an ADR documents the decision — and whether
   a companion ADR should exist in the OTHER repo too, per this fleet's
   companion-ADR convention (see `.claude/skills/how-to-write-an-adr/SKILL.md`).
7. **Auth-reintroduction and sibling-call bans**, same as `/code-review`
   items 6-8, but reasoned about more thoroughly here — check not just
   "is there a Bearer literal" but "does this change's INTENT require
   re-litigating the fleet-wide auth-removal decision," which would need
   its own ADR, not a silent code change.

## Output format

State clearly: PASS (no architectural concerns), CONCERNS (list them,
each tied to the specific rule/ADR it would violate), or NEEDS-ADR (the
change is architecturally sound but undocumented — name what the ADR
should cover). Cite the specific file/rule/ADR for every finding; a
finding with no citation is not actionable.

This is advisory. It never blocks a merge on its own, and it never
modifies files. If a finding conflicts with a decision explicitly stated
in this repo's own AGENTS.md/CLAUDE.md, defer to that document and say
so — this command reasons about the fleet's general conventions, not a
higher authority than the repo's own explicit guidance.
