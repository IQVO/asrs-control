# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **Aisle**, **Level**, **Slot**, **Depth** — the rack structure; depth 1 is the front, 2 the back (double-deep).
- **RackPosition** (`layout.Position`) — one storage position for one container, inside this context only; never a
  `LocationCode`, never published.
- **Port** — an INPUT or OUTPUT point of one aisle; the handoff point for material-flow-control.
- **StorageJob** — one STORE or RETRIEVE requested by material-flow-control, or one internal RESHUFFLE.
- **Reshuffle** — an internal MOVE of a front container that blocks a back position; invisible outside.
- **EquipmentUnit** — the combined shuttle-and-lift unit of one aisle (unit id `A<aisle>`), PackML state
  IDLE / EXECUTE / HELD.
- **Velocity** — FAST or SLOW, from this context's own retrieval history of the container (never its contents).
- **Telegram** — a message to or from the PLC with a handshake (ACK, 2 s retransmit, dedup by `telegramId`).

## Aggregates

- **StorageMap** (`internal/domain/storagemap`, one per aisle): one container per position and one position per
  container (inv. 1); a reserved position is never assigned (inv. 2); depth rules D1/D2; reshuffles change only the
  map (inv. 6); which container waits on each output port.
- **StorageJob** (`internal/domain/storagejob`): QUEUED → DISPATCHED → ACKNOWLEDGED → DONE, a retrieve via
  AWAITING_ARRIVAL; any open job → FAILED; CANCEL_REQUESTED → CANCELLED. Exactly one outcome (inv. 5).
- **EquipmentUnit** (`internal/domain/equipment`): at most one job in the PLC per unit (inv. 3); PackML state.
- `scheduling.Next` (`internal/domain/scheduling`) decides an aisle's next action; retrieves to one port never
  overtake each other (inv. 4).

## Domain events

- `ContainerStored` — a STORE's JOB_DONE. `ContainerPresentedAtPort` — a RETRIEVE's PORT_ARRIVAL (scan READ,
  NO_READ or MISMATCH). `StorageJobFailed` — any external job's failure or cancellation, with a reason.
- `EquipmentStateChanged` — a UNIT_STATE that changes a unit's state. `AsrsCapacityChanged` — units available,
  free positions or open jobs of an AS/RS changed.

## Key use cases (internal/application/usecases, one `Service`)

- `RequestStore`, `RequestRetrieve`, `Cancel`, `ConfirmPickup` — REST commands; each runs in one aisle unit of
  work and ends with the scheduler.
- `HandleUp`, `HandleAck`, `HandleExpired` — the MQTT link's telegrams; `Recover` — boot recovery.
- `Job`, `Asrs` — queries.
