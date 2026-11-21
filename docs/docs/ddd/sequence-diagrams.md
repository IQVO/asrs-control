---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
sidebar_position: 9
description: "The telegram handshake, a duplicate, a cancel, an unanswered command and boot recovery."
---

# Sequence diagrams

## Handshake and duplicate

```mermaid
sequenceDiagram
  participant A as asrs-control
  participant DB as Postgres
  participant P as PLC
  P->>A: JOB_DONE (telegramId T1)
  A->>DB: BEGIN; lock aisle; claim T1 with ACK id K1; apply; COMMIT
  A->>P: ACK (K1, ackTelegramId T1)
  Note over P: the ACK was lost; the PLC retransmits after 2 s
  P->>A: JOB_DONE (T1 again)
  A->>DB: claim T1: already claimed with K1, change nothing
  A->>P: ACK (K1) again
```

## Cancel a job the PLC holds

```mermaid
sequenceDiagram
  participant MFC as material-flow-control
  participant A as asrs-control
  participant P as PLC
  MFC->>A: POST .../cancellation
  A-->>MFC: 202 CANCEL_REQUESTED
  A->>P: CANCEL (jobId)
  alt accepted
    P->>A: ACK accepted
    Note over A: CANCELLED, StorageJobFailed reason CANCELLED
  else refused or no ACK after 3 retransmits
    P->>A: ACK refused
    Note over A: reverted to ACKNOWLEDGED (or DISPATCHED); the job runs to its outcome
  end
```

## A command the PLC never answers

```mermaid
sequenceDiagram
  participant A as asrs-control
  participant P as PLC
  A->>P: RETRIEVE (T2)
  A->>P: RETRIEVE (T2) after 2 s
  A->>P: RETRIEVE (T2) after 4 s
  A->>P: RETRIEVE (T2) after 6 s
  Note over A: expiry: the job fails EQUIPMENT_UNREACHABLE, the unit is free, the next job may go
```

## Boot recovery

```mermaid
sequenceDiagram
  participant A as asrs-control
  participant DB as Postgres
  participant P as PLC
  A->>DB: migrations, Prepare (maps and units)
  A->>P: subscribe asrs/v1/ASRS1/up
  A->>DB: in-flight commands of ASRS1
  A->>P: resend every unacknowledged command (same telegramId), every pending CANCEL
  A->>P: STATE_REQUEST
  P->>A: UNIT_STATE per unit
  Note over A: schedule every aisle, start the relay, serve HTTP
```
