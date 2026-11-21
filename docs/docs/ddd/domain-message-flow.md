---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
sidebar_position: 5
description: "A store and a retrieve with a reshuffle, message by message, from material-flow-control to the PLC and back."
---

# Domain Message Flow

Following the ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).

## Store

```mermaid
sequenceDiagram
  participant MFC as material-flow-control
  participant A as asrs-control
  participant P as PLC
  participant K as Kafka
  MFC->>A: POST storage-jobs (STORE, C-0001, IN1)
  A-->>MFC: 202 job QUEUED or DISPATCHED (position reserved)
  A->>P: STORE (jobId, containerId, IN1, to)
  P->>A: ACK accepted
  P->>A: JOB_DONE
  A->>P: ACK
  A->>K: ContainerStored
```

## Retrieve behind a blocking container

```mermaid
sequenceDiagram
  participant MFC as material-flow-control
  participant A as asrs-control
  participant P as PLC
  participant K as Kafka
  MFC->>A: POST storage-jobs (RETRIEVE, C-0002, OUT1)
  A-->>MFC: 202 job
  Note over A: C-0002 is at depth 2, its front is occupied: reshuffle first
  A->>P: MOVE (front container to a free position)
  P->>A: ACK, JOB_DONE
  A->>P: ACK, then RETRIEVE (C-0002, from, OUT1)
  P->>A: ACK, JOB_DONE, PORT_ARRIVAL (C-0002)
  A->>P: ACKs
  A->>K: ContainerPresentedAtPort (scan READ)
  MFC->>A: POST ports/OUT1/pickups (C-0002)
```

The MOVE publishes nothing (invariant 6). Every event goes to both `warehouse.asrs-control.events` and
`warehouse.asrs-control.analytics`; `AsrsCapacityChanged` follows whenever units available, free positions or open jobs
change.
