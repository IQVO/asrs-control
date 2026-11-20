package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

const problemBaseURI = "https://errors.asrs-control.warehouse-systems.dev/"

// replayClass is the idempotency class of a problem type (docs/idempotency-replay-classes.md).
type replayClass string

// No problem type of this service is OUTCOME (no refusal commits state), so only two classes
// are declared; a future OUTCOME refusal adds the constant and the middleware's KeepWrites path.
const (
	classFinal     replayClass = "FINAL"
	classRetryable replayClass = "RETRYABLE"
)

type problem struct {
	status int
	slug   string
	title  string
	class  replayClass
}

var (
	errMalformedRequest = errors.New("request body is not valid JSON for this operation")
	errInvalidParameter = errors.New("parameter is invalid")
)

var (
	internalProblem        = problem{http.StatusInternalServerError, "internal-error", "Internal server error", classRetryable}
	dependencyUnavailable  = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable", classRetryable}
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", classFinal}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", classFinal}
	// invalidParameter is shared by the adapter's own bound checks and usecases.ErrInvalidRequest.
	invalidParameter = problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", classFinal}
)

// problemCatalogue maps every typed error to its problem, in match order.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "bad-request", "Malformed request", classFinal}},
	{errInvalidParameter, invalidParameter},
	{usecases.ErrInvalidRequest, invalidParameter},
	{usecases.ErrUnknownAsrs, problem{http.StatusNotFound, "asrs-not-found", "AS/RS not found", classFinal}},
	{usecases.ErrJobNotFound, problem{http.StatusNotFound, "storage-job-not-found", "Storage job not found", classFinal}},
	{usecases.ErrUnknownPort, problem{http.StatusUnprocessableEntity, "unknown-port", "The port is not a port of this AS/RS", classFinal}},
	{usecases.ErrWrongPortKind, problem{http.StatusUnprocessableEntity, "wrong-port-kind", "A store uses an INPUT port, a retrieve and a pickup an OUTPUT port", classFinal}},
	{usecases.ErrUnknownContainer, problem{http.StatusUnprocessableEntity, "unknown-container", "The container is not stored in this AS/RS", classRetryable}},
	{usecases.ErrContainerBusy, problem{http.StatusUnprocessableEntity, "container-busy", "The container already has an open storage job", classRetryable}},
	{usecases.ErrContainerAlreadyStored, problem{http.StatusUnprocessableEntity, "container-already-stored", "The container is already stored in this AS/RS", classRetryable}},
	{usecases.ErrStorageFull, problem{http.StatusUnprocessableEntity, "storage-full", "The aisle has no assignable rack position", classRetryable}},
	{usecases.ErrAisleUnavailable, problem{http.StatusUnprocessableEntity, "aisle-unavailable", "The aisle's shuttle-and-lift unit is held", classRetryable}},
	{usecases.ErrPortUnreachable, problem{http.StatusUnprocessableEntity, "port-unreachable", "The output port is not in the container's aisle", classRetryable}},
	{usecases.ErrPortNotPresenting, problem{http.StatusConflict, "port-not-presenting", "The output port does not hold that container", classRetryable}},
	{storagejob.ErrNotCancellable, problem{http.StatusConflict, "job-not-cancellable", "The container is already on its way to the port", classRetryable}},
	{storagejob.ErrJobEnded, problem{http.StatusConflict, "job-ended", "The storage job has already ended", classRetryable}},
}

func problemFor(err error) problem {
	for _, e := range problemCatalogue {
		if errors.Is(err, e.err) {
			return e.p
		}
	}
	return internalProblem
}
