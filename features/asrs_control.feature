Feature: asrs-control stores, retrieves and reshuffles containers in a double-deep AS/RS
  The rack has 1 aisle, 1 level and 2 slots, each slot double-deep: four positions. The rules are
  the asrs-control context page's invariants 1-6 and plan 02's decisions D3-D9.

  Background:
    Given an AS/RS "ASRS1" with 1 aisle, 1 level and 2 slots, double-deep

  Scenario: a stored container is reported once
    When container "A" is stored through "IN1"
    Then the job of "A" is "DONE"
    And 1 "ContainerStored" event was published

  Scenario: the fronts are not assigned while both backs wait for their stores (depth rule D2)
    When container "A" is requested for storage through "IN1"
    And container "B" is requested for storage through "IN1"
    And container "C" is requested for storage through "IN1"
    Then the request is refused with "storage-full"

  Scenario: a blocked back container is reshuffled first and the move publishes nothing
    Given containers "A", "B" and "C" are stored through "IN1"
    When container "A" is requested for retrieval
    Then the next command is a "MOVE" of "C"
    When the PLC completes the pending command
    Then the next command is a "RETRIEVE" of "A"
    When the PLC completes the pending command
    And the output port "OUT1" reads "A"
    Then the job of "A" is "DONE"
    And 1 "ContainerPresentedAtPort" event was published
    And the only events naming "C" are "ContainerStored"

  Scenario: retrieves to one port are presented in request order
    Given containers "A" and "B" are stored through "IN1"
    When container "A" is requested for retrieval
    And container "B" is requested for retrieval
    And the PLC completes the pending command
    And the output port "OUT1" reads "A"
    Then the job of "B" is "QUEUED"
    When the pickup of "A" at "OUT1" is confirmed
    Then the next command is a "RETRIEVE" of "B"

  Scenario: a held unit fails the queued jobs with EQUIPMENT_FAULT
    When container "A" is requested for storage through "IN1"
    And container "B" is requested for storage through "IN1"
    And the unit "A1" reports "HELD"
    Then the job of "B" failed with "EQUIPMENT_FAULT"
    And the job of "A" is "DISPATCHED"
    And 1 "EquipmentStateChanged" event was published
    When container "C" is requested for storage through "IN1"
    Then the request is refused with "aisle-unavailable"

  Scenario: a duplicate JOB_DONE is applied once
    When container "A" is requested for storage through "IN1"
    And the PLC acknowledges the pending command
    And the PLC reports JOB_DONE for "A" twice with the same telegram id
    Then 1 "ContainerStored" event was published
    And both telegrams got the same ACK

  Scenario: a refused command fails the job with COMMAND_REFUSED
    When container "A" is requested for storage through "IN1"
    And the PLC refuses the pending command with "INVALID_POSITION"
    Then the job of "A" failed with "COMMAND_REFUSED"

  Scenario: a command the PLC never acknowledges fails the job with EQUIPMENT_UNREACHABLE
    When container "A" is requested for storage through "IN1"
    And the pending command expires unacknowledged
    Then the job of "A" failed with "EQUIPMENT_UNREACHABLE"

  Scenario: cancelling a queued job ends it at once
    When container "A" is requested for storage through "IN1"
    And container "B" is requested for storage through "IN1"
    And the job of "B" is cancelled
    Then the job of "B" failed with "CANCELLED"
    And 1 "StorageJobFailed" event was published

  Scenario: a refused cancellation lets the job run to its outcome
    When container "A" is requested for storage through "IN1"
    And the PLC acknowledges the pending command
    And the job of "A" is cancelled
    Then the next command is a "CANCEL" of "A"
    When the PLC refuses the pending command with "JOB_RUNNING"
    Then the job of "A" is "ACKNOWLEDGED"
