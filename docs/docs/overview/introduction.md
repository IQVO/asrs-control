---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What asrs-control owns and does not own, its Generic classification in the WCS tier, its capabilities and where to read next."
---

# asrs-control

**asrs-control** is the AS/RS controller of the warehouse-systems WCS tier: the subsystem controller of the automated
storage and retrieval system. `material-flow-control` asks it to **store** a container waiting on an input port or to
**retrieve** a stored container to an output port. asrs-control picks the **rack position** (velocity zone, double-deep
depth rules), keeps the queue of **storage jobs** and gives each aisle's shuttle-and-lift unit **one job at a time**,
moves a blocking front container out of the way (**reshuffle**), presents retrieved containers at each output port **in
request order**, tracks every unit's **PackML state** and the AS/RS **capacity**, and talks to the PLC in **telegram
protocol v1** over MQTT. To the rest of the fleet the whole AS/RS is one logical location: no rack position, reshuffle or
internal move ever leaves the context.

| | |
| --- | --- |
| Tier | WCS (warehouse control), CloudEvents segment `wes` |
| Subdomain class | **Generic**, built behind the subsystem-controller interface so a vendor controller could replace it ([Classification](/docs/ddd/subdomain-classification)) |
| Repository | [`IQVO/asrs-control`](https://github.com/IQVO/asrs-control), Go, hexagonal |
| Binary | `cmd/api`: REST, one MQTT link per AS/RS, the transactional-outbox relay and the probes |
| Store, bus and link | Postgres (owned by the context), Kafka (the fleet broker, publish only), MQTT (the PLC link) |
| Design source | [asrs-control in the WCS strategic design](https://iqvo.github.io/warehouse-docs/wcs/asrs-control) (warehouse-docs); this repository's [ADRs](/docs/adr) record what is built |

## What it owns

| Concern | Where |
| --- | --- |
| Which container sits in which rack position, per aisle; reservations; the container waiting on each output port | `StorageMap` aggregate, `internal/domain/storagemap` |
| Storage position assignment by velocity zone (FAST front and cheap, SLOW back and dear) with depth rules D1/D2 | `storagemap.Assign`, `usecases.VelocityPolicy` |
| Store, retrieve and reshuffle jobs, their order and their single outcome | `StorageJob` aggregate, `internal/domain/storagejob` |
| What an aisle does next (dispatch, reshuffle first, wait for the port, fail) | `scheduling.Next`, `internal/domain/scheduling` |
| One job at a time per aisle unit and its PackML state (IDLE, EXECUTE, HELD) | `EquipmentUnit` aggregate, `internal/domain/equipment` |
| Capacity facts: units available, positions free, open jobs | `internal/domain/capacity` |
| The AS/RS structure (aisles, levels, slots, depth, ports, travel cost) | the layout file, `internal/domain/layout` ([ADR 0002](/docs/adr/0002-telegram-conformance-and-aisle-scheduling)) |

## What it does not own

| Concern | Owner |
| --- | --- |
| SKUs and quantities in a container, the stock ledger | `inventory-storage` |
| Which container to retrieve, and when | `fulfillment-execution` and `wes-work-planning`, through `material-flow-control` |
| Transport beyond its own ports | `material-flow-control` and `amr-fleet-control` |
| The AS/RS as a location in the building | `facility-layout` (one `AutomatedStorage` location) |
| The PLC itself | the equipment (in this study, [IQVO/equipment-emulator](https://github.com/IQVO/equipment-emulator)) |

## Guarantees (invariants of the context page)

1. One container per rack position, and every stored container is in exactly one position.
2. A reserved position is never assigned.
3. An aisle's shuttle-and-lift unit runs one job at a time.
4. Retrieved containers reach each output port in the requested sequence.
5. Every accepted job ends in exactly one of DONE, FAILED or CANCELLED, with a reason.
6. Internal moves are invisible outside: no transport fact, never the stock ledger.

## Built and planned

Built on `develop` (2026-11-20, PR #2): the REST subsystem-controller interface, telegram protocol v1 over MQTT, five
CloudEvents types on `warehouse.asrs-control.events` and `.analytics`, Postgres persistence with a transactional outbox,
the Helm chart, and the fleet OpenTelemetry standard (traces, metrics, trace-correlated logs,
[ADR 0004](/docs/adr/0004-adopt-the-fleet-opentelemetry-standard)). Planned and **not built**: an MCP server and an A2A
adapter, Kong routes in the chart, the registration in warehouse-infra, and a protocol v2 job-state query (the known limits
of ADR 0002).

## Read next

- [Architecture](/docs/overview/architecture) and [Quickstart](/docs/overview/quickstart)
- [Ubiquitous language](/docs/ddd/ubiquitous-language) and [Use cases](/docs/ddd/use-cases)
- [API overview](/docs/api-reference) and the [event and telegram catalogue](/docs/api-reference/events)
- [Runbook](/docs/operations/runbook)
