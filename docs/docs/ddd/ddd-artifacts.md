---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts
sidebar_position: 4
description: "The ddd-crew artifact pack of asrs-control and where each artifact's facts come from."
---

# DDD artifacts (ddd-crew)

The pack follows the [ddd-crew](https://github.com/ddd-crew) templates. Every identifier, state, event and reason in it is
taken from the code on `develop`; the strategic placement comes from the [WCS strategic design](https://iqvo.github.io/warehouse-docs/wcs) in warehouse-docs.

| Artifact | What it shows |
| --- | --- |
| [Core Domain Chart](/docs/ddd/core-domain-chart) | Why the context is Generic |
| [Bounded Context Canvas](/docs/ddd/bounded-context-canvas) | Purpose, language, decisions, inbound and outbound communication |
| [Context Map](/docs/ddd/context-map) | The relationships with material-flow-control, the PLC and exception-management |
| [Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas) | StorageMap, StorageJob and EquipmentUnit |
| [Domain Message Flow](/docs/ddd/domain-message-flow) | A store and a retrieve end to end |
| [EventStorming](/docs/ddd/eventstorming) | Commands, policies and events of the process |
| [Class diagram](/docs/ddd/class-diagram) | The domain types |
| [Entity relationship](/docs/ddd/entity-relationship) | The Postgres schema |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | Handshake, cancel, recovery |
| [Domain events](/docs/ddd/domain-events) | Every published event and its fields |
