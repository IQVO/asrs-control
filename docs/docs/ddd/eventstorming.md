---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 6
description: "The process-level EventStorming of asrs-control: commands, policies, events and hot spots."
---

# EventStorming

Process level, read left to right. Commands are blue, events orange, policies lilac, external systems pink.

```mermaid
flowchart LR
  classDef cmd fill:#bfdbfe,stroke:#1d4ed8,color:#111
  classDef evt fill:#fed7aa,stroke:#c2410c,color:#111
  classDef pol fill:#e9d5ff,stroke:#7e22ce,color:#111
  classDef ext fill:#fbcfe8,stroke:#be185d,color:#111
  MFC[material-flow-control]:::ext --> RS[Request store]:::cmd --> JA[Store job accepted, position reserved — internal]:::evt
  MFC --> RR[Request retrieve]:::cmd --> RA[Retrieve job accepted — internal]:::evt
  JA --> SP{{Whenever the aisle unit is free: dispatch the oldest job}}:::pol
  RA --> SP
  SP --> BL{{Blocked back position: reshuffle first}}:::pol --> MV[Container moved, internal]:::evt
  SP --> DT[Command telegram sent]:::evt --> PLC[PLC]:::ext
  PLC --> JD[JOB_DONE received]:::evt
  JD --> CS[ContainerStored]:::evt
  JD --> PA[PORT_ARRIVAL received]:::evt --> CP[ContainerPresentedAtPort]:::evt --> PU[Confirm pickup]:::cmd
  PLC --> JF[JOB_FAILED or refused ACK or no ACK]:::evt --> SF[StorageJobFailed]:::evt
  PLC --> US[UNIT_STATE changed]:::evt --> EC[EquipmentStateChanged]:::evt
  US --> HP{{HELD: fail the aisle's queued jobs}}:::pol --> SF
  MFC --> CN[Cancel job]:::cmd --> SF
```

## Hot spots

- No job-state query in protocol v1: a JOB_DONE lost while asrs-control is down longer than about 8 s leaves the job
  ACKNOWLEDGED (ADR 0002 known limits).
- A CANCEL the PLC accepted whose ACK and retransmits are all lost leaves the job waiting; deliberately not auto-failed.
- No telegram reports a container leaving an output port: the pickup confirmation is the only signal.
