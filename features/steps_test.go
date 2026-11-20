package features_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

type world struct {
	ctx    context.Context
	svc    *usecases.Service
	jobs   *memory.StorageJobs
	events *memory.Events
	down   *memory.Downlink
	asrs   string
	byBox  map[string]string // container -> its latest job id
	err    error
	acks   []string
	seq    int
}

var slugs = map[error]string{
	usecases.ErrStorageFull: "storage-full", usecases.ErrAisleUnavailable: "aisle-unavailable",
	usecases.ErrContainerBusy: "container-busy", usecases.ErrUnknownContainer: "unknown-container",
}

func (w *world) anAsrs(id string, aisles, levels, slots int) error {
	l := layout.Layout{ID: id, Aisles: aisles, Levels: levels, Slots: slots, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1}}
	for a := 1; a <= aisles; a++ {
		l.Ports = append(l.Ports, layout.Port{ID: "IN" + strconv.Itoa(a), Aisle: a, Kind: layout.PortInput},
			layout.Port{ID: "OUT" + strconv.Itoa(a), Aisle: a, Kind: layout.PortOutput})
	}
	st := memory.NewStore()
	w.ctx, w.asrs, w.byBox = context.Background(), id, map[string]string{}
	w.jobs, w.events, w.down = memory.NewStorageJobs(st), memory.NewEvents(st), memory.NewDownlink()
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{l}, UoW: memory.NewUnitOfWork(st), Maps: memory.NewStorageMaps(st),
		Jobs: w.jobs, Units: memory.NewUnits(st), Capacity: memory.NewCapacities(st), Telegrams: memory.NewTelegrams(st),
		Events: w.events, Downlink: w.down, Clock: memory.NewClock(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)),
		IDs: memory.NewIDs("id-"), Velocity: usecases.VelocityPolicy{Window: 24 * time.Hour, FastRetrievals: 3}})
	if err != nil {
		return err
	}
	w.svc = svc
	return svc.Prepare(w.ctx)
}

func (w *world) requestStore(c, port string) error {
	j, err := w.svc.RequestStore(w.ctx, usecases.StoreRequest{AsrsID: w.asrs, ContainerID: c, PortID: port})
	w.err = err
	if err == nil {
		w.byBox[c] = j.ID()
	}
	return nil
}

func (w *world) requestRetrieve(c string) error {
	j, err := w.svc.RequestRetrieve(w.ctx, usecases.RetrieveRequest{AsrsID: w.asrs, ContainerID: c})
	if err != nil {
		return err
	}
	w.byBox[c] = j.ID()
	return nil
}

func (w *world) stored(c, port string) error {
	if err := w.requestStore(c, port); err != nil || w.err != nil {
		return errors.Join(err, w.err)
	}
	return w.completePending()
}

func (w *world) storedMany(a, b, c, port string) error {
	for _, x := range []string{a, b, c} {
		if x == "" {
			continue
		}
		if err := w.stored(x, port); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) storedTwo(a, b, port string) error { return w.storedMany(a, b, "", port) }

func (w *world) pending() telegrams.Command { return w.down.Last(w.asrs) }

func (w *world) ackPending() error {
	return w.svc.HandleAck(w.ctx, w.asrs, w.pending().TelegramID, true, "")
}

func (w *world) up(t telegrams.UpTelegram) (string, error) {
	if t.TelegramID == "" {
		w.seq++
		t.TelegramID = "up-" + strconv.Itoa(w.seq)
	}
	t.Timestamp = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	return w.svc.HandleUp(w.ctx, w.asrs, t)
}

func (w *world) completePending() error {
	cmd := w.pending()
	if err := w.ackPending(); err != nil {
		return err
	}
	_, err := w.up(telegrams.UpTelegram{Type: telegrams.UpJobDone, JobID: cmd.JobID})
	return err
}

func (w *world) portReads(port, c string) error {
	_, err := w.up(telegrams.UpTelegram{Type: telegrams.UpPortArrival, PortID: port, ContainerID: c})
	return err
}

func (w *world) pickup(c, port string) error { return w.svc.ConfirmPickup(w.ctx, w.asrs, port, c) }

func (w *world) unitReports(unit, state string) error {
	_, err := w.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: unit, State: state})
	return err
}

func (w *world) duplicateDone(c string) error {
	t := telegrams.UpTelegram{TelegramID: "dup-1", Type: telegrams.UpJobDone, JobID: w.byBox[c]}
	for range 2 {
		ack, err := w.up(t)
		if err != nil {
			return err
		}
		w.acks = append(w.acks, ack)
	}
	return nil
}

func (w *world) sameAck() error {
	if len(w.acks) != 2 || w.acks[0] == "" || w.acks[0] != w.acks[1] {
		return fmt.Errorf("ACK ids %v, want two equal ids", w.acks)
	}
	return nil
}

func (w *world) refusePending(reason string) error {
	return w.svc.HandleAck(w.ctx, w.asrs, w.pending().TelegramID, false, reason)
}

func (w *world) expirePending() error {
	return w.svc.HandleExpired(w.ctx, w.asrs, w.pending().TelegramID)
}

func (w *world) cancel(c string) error {
	_, err := w.svc.Cancel(w.ctx, w.asrs, w.byBox[c])
	return err
}

func (w *world) job(c string) (*storagejob.Job, error) { return w.jobs.Get(w.ctx, w.byBox[c]) }

func (w *world) jobIs(c, status string) error {
	j, err := w.job(c)
	if err != nil {
		return err
	}
	if string(j.Status()) != status {
		return fmt.Errorf("job of %s is %s, want %s", c, j.Status(), status)
	}
	return nil
}

func (w *world) jobFailedWith(c, reason string) error {
	j, err := w.job(c)
	if err != nil {
		return err
	}
	if j.Open() || string(j.Reason()) != reason {
		return fmt.Errorf("job of %s is %s/%s, want ended with %s", c, j.Status(), j.Reason(), reason)
	}
	return nil
}

func (w *world) refusedWith(slug string) error {
	for err, s := range slugs {
		if errors.Is(w.err, err) && s == slug {
			return nil
		}
	}
	return fmt.Errorf("the last request answered %v, want %s", w.err, slug)
}

func (w *world) nextCommand(typ, c string) error {
	cmd := w.pending()
	if string(cmd.Type) != typ || (cmd.ContainerID != c && cmd.JobID != w.byBox[c]) {
		return fmt.Errorf("next command = %s %s (job %s), want %s of %s", cmd.Type, cmd.ContainerID, cmd.JobID, typ, c)
	}
	return nil
}

func (w *world) eventCount(n int, name string) error {
	got := 0
	for _, e := range w.events.All() {
		if reflect.TypeOf(e).Name() == name {
			got++
		}
	}
	if got != n {
		return fmt.Errorf("%d %s events, want %d", got, name, n)
	}
	return nil
}

func (w *world) onlyEventsNaming(c, name string) error {
	for _, e := range w.events.All() {
		v := reflect.ValueOf(e)
		if f := v.FieldByName("ContainerID"); f.IsValid() && f.String() == c && v.Type().Name() != name {
			return fmt.Errorf("a %s names %s", v.Type().Name(), c)
		}
	}
	return nil
}

func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Step(`^an AS/RS "([^"]*)" with (\d+) aisles?, (\d+) levels? and (\d+) slots, double-deep$`, w.anAsrs)
	sc.Step(`^container "([^"]*)" is stored through "([^"]*)"$`, w.stored)
	sc.Step(`^containers "([^"]*)", "([^"]*)" and "([^"]*)" are stored through "([^"]*)"$`, w.storedMany)
	sc.Step(`^containers "([^"]*)" and "([^"]*)" are stored through "([^"]*)"$`, w.storedTwo)
	sc.Step(`^container "([^"]*)" is requested for storage through "([^"]*)"$`, w.requestStore)
	sc.Step(`^container "([^"]*)" is requested for retrieval$`, w.requestRetrieve)
	sc.Step(`^the PLC completes the pending command$`, w.completePending)
	sc.Step(`^the PLC acknowledges the pending command$`, w.ackPending)
	sc.Step(`^the PLC refuses the pending command with "([^"]*)"$`, w.refusePending)
	sc.Step(`^the pending command expires unacknowledged$`, w.expirePending)
	sc.Step(`^the PLC reports JOB_DONE for "([^"]*)" twice with the same telegram id$`, w.duplicateDone)
	sc.Step(`^both telegrams got the same ACK$`, w.sameAck)
	sc.Step(`^the output port "([^"]*)" reads "([^"]*)"$`, w.portReads)
	sc.Step(`^the pickup of "([^"]*)" at "([^"]*)" is confirmed$`, w.pickup)
	sc.Step(`^the unit "([^"]*)" reports "([^"]*)"$`, w.unitReports)
	sc.Step(`^the job of "([^"]*)" is cancelled$`, w.cancel)
	sc.Step(`^the job of "([^"]*)" is "([^"]*)"$`, w.jobIs)
	sc.Step(`^the job of "([^"]*)" failed with "([^"]*)"$`, w.jobFailedWith)
	sc.Step(`^the request is refused with "([^"]*)"$`, w.refusedWith)
	sc.Step(`^the next command is a "([^"]*)" of "([^"]*)"$`, w.nextCommand)
	sc.Step(`^(\d+) "([^"]*)" events? (?:was|were) published$`, w.eventCount)
	sc.Step(`^the only events naming "([^"]*)" are "([^"]*)"$`, w.onlyEventsNaming)
}
