---
name: domain-review
description: Ubiquitous-language drift review of a change against .claude/rules/domain-model.md: renamed or invented terms, aggregate-invariant leaks. Invoke explicitly: /domain-review [range].
disable-model-invocation: true
argument-hint: "[git range]"
---

Perform a ubiquitous-language drift review of the current changes (or `$ARGUMENTS` if given),
comparing new or changed code against `.claude/rules/domain-model.md` and the site's
`docs/docs/ddd/ubiquitous-language.md`.

Drift is code that works but silently renames, reshapes or duplicates a concept the model already
names, so readers can no longer map code to the domain conversation.

## What to check, in priority order

1. **A duplicate of an existing concept under another name.** The model names *RackPosition*
   (`layout.Position`), *Reservation*, *Velocity* (FAST/SLOW), *Presentation*, *Pickup*,
   *Reshuffle*. A new `Slot`/`Location`/`Bin` type for a position, a `free` count computed outside
   `storagemap.Map.Free`, or a "priority" that is really velocity is drift: point at the existing
   name.
2. **Implementation terms in the domain.** `internal/domain/` reads like the model:
   `ReserveStore`, `CompleteMotion`, `Present`, `RequestCancel`, `Report`. A method called
   `UpdateRow`, `SetStatus` or `HandleMessage` there is drift (fitness tests check imports, not
   names).
3. **Telegram vocabulary leaking up.** Telegram types (STORE, JOB_DONE, ACK, PORT_ARRIVAL) belong to
   the adapter and `internal/application/telegrams`; the domain speaks of jobs, motions,
   presentations and unit states. A domain type named after a telegram is drift.
4. **An invariant in code but not in the model, or the reverse.** Invariants 1-6, depth rules D1 and
   D2, "one job per aisle unit", "HELD fails queued jobs", "retrieves to one port in acceptance
   order". A new guard needs its line in `.claude/rules/domain-model.md` in the same PR.
5. **Closed sets opened (or the reverse).** Job statuses, failure reasons, PackML states (IDLE,
   EXECUTE, HELD only: protocol v1 sends no other) and scan results are closed enums mirrored in
   `apis/*.yaml`. Adding a value is a contract change (reason enums are pinned by consumers).
6. **Invariant 6 in language.** A term that only makes sense inside the rack (position, depth,
   reshuffle) appearing in a REST DTO or an event payload is a boundary leak, not just a naming
   issue.
7. **New terminology without a model entry.** A genuinely new concept needs its row in the
   domain-model rule and the ubiquitous-language page in the same PR.

## Output format

For each finding: the term, where the model defines it (or "not yet documented"), where the drift
is in code, and a one-sentence recommendation (rename, document, or confirm a new concept). If the
change uses the existing vocabulary correctly, say so plainly.

This command never modifies files. It is advisory input for the author, not a blocking gate.
