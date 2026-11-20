---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 3
description: "Why asrs-control is a Generic subdomain of the WCS tier and what that decides."
---

# Subdomain classification

asrs-control is **Generic** ([WCS subdomains](https://iqvo.github.io/warehouse-docs/wcs/subdomains), warehouse-docs;
[ADR 0001](/docs/adr/0001-asrs-control-bounded-context)).

| Question | Answer |
| --- | --- |
| Does it differentiate the business? | No. Every AS/RS ships with a controller that assigns positions, sequences shuttle and lift jobs and presents containers at ports. |
| Is the model complex? | Moderately: double-deep depth rules, reshuffles, a handshake protocol with retransmits and recovery. |
| Build, buy or adopt? | In a real warehouse: buy (the vendor's controller). Here it is built, because the study has no vendor, and built **behind the subsystem-controller interface** so a vendor controller could replace it without touching material-flow-control. |

## What the classification decides

- The REST interface and the five events are the **subsystem-controller interface**, shaped for any AS/RS controller, not
  for this implementation: no rack position, reshuffle or internal move crosses it (invariant 6, seam decision 2).
- The telegram protocol is **Conformist** to the equipment's protocol (telegram protocol v1 of the emulator), not
  designed here ([ADR 0002](/docs/adr/0002-telegram-conformance-and-aisle-scheduling)).
- The context calls no sibling and consumes nothing from Kafka: everything it needs is its layout file, its own history and
  the PLC.

Siblings in the tier: `material-flow-control` (Supporting, the orchestrator) and `amr-fleet-control` (Generic, the other
subsystem controller). See the [Core Domain Chart](/docs/ddd/core-domain-chart).
