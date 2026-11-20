---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
sidebar_position: 7
description: "The domain types of asrs-control and how they relate."
---

# Class diagram

```mermaid
classDiagram
  class Layout {
    ID string
    Aisles, Levels, Slots int
    DoubleDeep bool
    Ports []Port
    Cost Cost
  }
  class Port {
    ID string
    Aisle int
    Kind PortKind
  }
  class Position {
    Aisle, Level, Slot, Depth int
    Front() Position
    Back() Position
  }
  class Map {
    ReserveStore(jobID, container, v) Position
    ReserveRetrieve(jobID, container)
    PlanReshuffle(jobID, blocked, v)
    CompleteStore() CompleteRetrieve() CompleteMove()
    Pickup(port, container)
    Assign(v, exclude) Position
  }
  class Job {
    Kind STORE/RETRIEVE/RESHUFFLE
    Status
    Reason
    Seq int64
    Dispatch(telegramID)
    Acknowledge()
    CompleteMotion() []event
    Present(scanned) []event
    Fail(Failure) []event
    RequestCancel(telegramID)
    ConfirmCancel() RevertCancel()
  }
  class Unit {
    UnitID string
    State IDLE/EXECUTE/HELD
    CurrentJob string
    Assign(jobID) Release(jobID)
    Report(state) EquipmentStateChanged
  }
  class Snapshot {
    UnitsTotal, UnitsAvailable int
    PositionsTotal, PositionsFree int
    OpenJobs int
  }
  class Next {
    <<domain service>>
    Next(map, unit, open) Decision
  }
  Layout "1" o-- "*" Port
  Layout "1" --> "*" Map : one per aisle
  Map --> Position
  Job --> Position : from, to
  Layout "1" --> "*" Unit : one per aisle
  Next ..> Map
  Next ..> Unit
  Next ..> Job
  Job --> Job : reshuffle ParentID
  Snapshot ..> Layout
```

Events are plain structs next to their aggregates: `storagejob.ContainerStored`, `ContainerPresentedAtPort`,
`StorageJobFailed`, `equipment.EquipmentStateChanged`, `capacity.AsrsCapacityChanged`.
