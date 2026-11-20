# 2. Telegram conformance, the layout file and one job per aisle unit

Status: Accepted (2026-11-20). Resolves the handoff's open question "The AS/RS telegram protocol: asrs-control ADR
0001" — the protocol is fixed by IQVO/equipment-emulator `docs/telegram-protocol.md` (v1) and this ADR records how
asrs-control conforms.

## Decisions

1. **Conformist.** asrs-control implements its own codec and retransmitter (`internal/adapters/telegram`) that
   mirror the emulator's reference code: flat JSON, `timestamp` `2006-01-02T15:04:05.000Z`, QoS 1, not retained.
   It ACKs every non-ACK up telegram after the effect committed; a telegram it cannot decode but that carries a
   `telegramId` is ACKed `accepted:false, reason:MALFORMED`. Up telegrams are claimed in `processed_telegrams` in the
   effect's transaction with the ACK id, so a duplicate gets the identical ACK, also after a restart. Down
   telegrams are published only after the outermost unit of work committed (a `UnitOfWork.AfterCommit` hook) and
   are retransmitted with the same `telegramId` after 2 s real time, at most 3 times. An expiry of a command the PLC
   never acknowledged fails its job with `EQUIPMENT_UNREACHABLE` (also while a CANCEL is pending); an expired or
   refused CANCEL is reverted to DISPATCHED or ACKNOWLEDGED, by whether the command was acknowledged, so a cancel
   never leaves an aisle waiting on an unanswered command. Boot recovery resends every unacknowledged command and
   every pending CANCEL. `NOT_READY` is transient: the telegram is tracked again and resent, never an outcome.
2. **Ordering.** paho runs with `OrderMatters(true)`; its callback only enqueues; one worker per link handles up
   telegrams and expiries in order (plan 01 review: a mutex does not preserve order with paho).
3. **One job per aisle unit.** asrs-control keeps the queue. The PLC receives the next job of an aisle only after
   the previous one's JOB_DONE or JOB_FAILED. Invariant 3 is then true by construction, the PLC's start-time
   prechecks see current state, a cancellation of a queued job needs no telegram, and boot recovery has at most one
   command per aisle to resend. Every write locks its aisle row (`SELECT … FOR UPDATE`), so the map, the unit and
   the aisle's jobs change serially. Cost: no overlap between consecutive jobs of one aisle.
4. **Positions reserved at acceptance**, depth rules D1/D2, velocity from own retrieval history, reshuffle chosen at
   dispatch (plan 02 decisions D4-D6).
5. **Output ports present in acceptance order**; the next retrieve to a port waits until the port is free, which
   material-flow-control confirms with `POST /asrs/{asrsId}/ports/{portId}/pickups` because no telegram reports a
   container leaving a port.
6. **HELD** fails the aisle's queued jobs with `EQUIPMENT_FAULT` and refuses new ones (`422 aisle-unavailable`);
   the job inside the PLC finishes after the fault clears.
7. **Layout file.** Each AS/RS's aisles, levels, slots, doubleDeep, ports and travel-cost weights come from the YAML
   file `ASRS_LAYOUT_FILE`, with the emulator world's field names (emulator-only timing and `nodeId` fields are
   accepted and ignored). Boot refuses a layout that no longer contains a stored container's position.

## Known limits

- A JOB_DONE the PLC gave up retransmitting while asrs-control was down longer than ~8 s leaves that job
  ACKNOWLEDGED: protocol v1 has no job-state query. A reconciliation telegram needs a protocol v2.
- A JOB_FAILED for POSITION_OCCUPIED / CONTAINER_NOT_AT_POSITION means the map disagrees with the rack; the job fails
  `STORAGE_MAP_MISMATCH` and the position is not quarantined.
