---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: "Run asrs-control locally against Postgres, Kafka, Mosquitto and the equipment emulator, and store and retrieve one container."
---

# Quickstart

asrs-control runs on real Postgres, Kafka and MQTT only: a missing `DATABASE_URL`, `KAFKA_BROKERS`, `MQTT_URL` or
`ASRS_LAYOUT_FILE` is a boot error that names the variable. The PLC is the
[equipment emulator](https://github.com/IQVO/equipment-emulator); its example world and asrs-control's example layout
describe the same AS/RS (`ASRS1`, two aisles, double-deep, ports `IN1`/`OUT1` and `IN2`/`OUT2`).

## 1. Infrastructure

```bash
docker run -d --name asrs-pg -p 5432:5432 -e POSTGRES_USER=asrs -e POSTGRES_PASSWORD=asrs -e POSTGRES_DB=asrs_control postgres:17
docker run -d --name asrs-kafka -p 9092:9092 apache/kafka:3.9.1
docker run -d --name asrs-mqtt -p 1883:1883 eclipse-mosquitto:2.0.20 mosquitto -c /mosquitto-no-auth.conf
```

## 2. The emulator (the PLC)

In an equipment-emulator checkout, on another HTTP port than asrs-control:

```bash
WORLD_FILE=config/world.example.yaml HTTP_ADDR=:8081 SIM_SPEED=10 go run ./cmd/emulator
```

## 3. asrs-control

```bash
DATABASE_URL='postgres://asrs:asrs@localhost:5432/asrs_control?sslmode=disable' \
KAFKA_BROKERS=localhost:9092 \
MQTT_URL=tcp://localhost:1883 \
ASRS_LAYOUT_FILE=config/layout.example.yaml \
go run ./cmd/api
```

The migrations run at boot. `GET /readyz` answers `200` once Postgres, Kafka and the MQTT link are reachable.

## 4. Store a container

Put a container on input port `IN1` in the emulator, then ask asrs-control to store it:

```bash
curl -s -X POST localhost:8081/asrs/ASRS1/ports/IN1/container -H 'Content-Type: application/json' \
  -d '{"containerId":"C-0001"}'
curl -s -X POST localhost:8080/asrs/ASRS1/storage-jobs -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: store-C-0001' -d '{"kind":"STORE","container_id":"C-0001","port_id":"IN1","reference":"leg-9"}'
```

The answer is `202` with the job (`status` QUEUED or DISPATCHED). Read it with
`GET /asrs/ASRS1/storage-jobs/<storage_job_id>`: after the PLC's JOB_DONE it is DONE and `ContainerStored` is on
`warehouse.asrs-control.events`.

## 5. Retrieve it

```bash
curl -s -X POST localhost:8080/asrs/ASRS1/storage-jobs -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: retrieve-C-0001' -d '{"kind":"RETRIEVE","container_id":"C-0001","port_id":"OUT1"}'
```

After the PLC's JOB_DONE and PORT_ARRIVAL the job is DONE and `ContainerPresentedAtPort` is published. The port stays
occupied until material-flow-control confirms the handoff:

```bash
curl -s -X POST localhost:8080/asrs/ASRS1/ports/OUT1/pickups -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: pickup-C-0001' -d '{"container_id":"C-0001"}'
```

`GET /asrs/ASRS1` shows the aisles, units, ports, the recommended input port and the capacity at any time.

## Watch the events

```bash
docker exec asrs-kafka /opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server localhost:9092 \
  --topic warehouse.asrs-control.events --from-beginning
```

## Clean up

```bash
docker rm -f asrs-pg asrs-kafka asrs-mqtt
```
