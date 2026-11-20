// Package telegrams holds the application's view of AS/RS telegram protocol v1: the down
// commands it wants sent and the up telegrams it handles. The MQTT adapter maps them to the wire.
package telegrams

import (
	"time"

	"github.com/IQVO/asrs-control/internal/domain/layout"
)

// CommandType is a down telegram type of telegram protocol v1.
type CommandType string

const (
	CmdStore        CommandType = "STORE"
	CmdRetrieve     CommandType = "RETRIEVE"
	CmdMove         CommandType = "MOVE"
	CmdCancel       CommandType = "CANCEL"
	CmdStateRequest CommandType = "STATE_REQUEST"
)

// Command is one down telegram the application wants sent; the adapter stamps and encodes it.
type Command struct {
	TelegramID                 string
	Type                       CommandType
	JobID, ContainerID, PortID string
	From, To                   *layout.Position
}

// Up telegram types the application handles.
const (
	UpJobDone     = "JOB_DONE"
	UpJobFailed   = "JOB_FAILED"
	UpUnitState   = "UNIT_STATE"
	UpPortArrival = "PORT_ARRIVAL"
)

// UpTelegram is one decoded up telegram (not an ACK). Timestamp is the PLC's simulated time.
type UpTelegram struct {
	TelegramID, Type, JobID, ContainerID, PortID, Reason, UnitID, State string
	Timestamp                                                           time.Time
}
