package http

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

func TestEveryRefusalMapsToItsDocumentedProblem(t *testing.T) {
	cases := []struct {
		err    error
		status int
		slug   string
		class  replayClass
	}{
		{usecases.ErrUnknownAsrs, http.StatusNotFound, "asrs-not-found", classFinal},
		{usecases.ErrJobNotFound, http.StatusNotFound, "storage-job-not-found", classFinal},
		{usecases.ErrUnknownPort, http.StatusUnprocessableEntity, "unknown-port", classFinal},
		{usecases.ErrWrongPortKind, http.StatusUnprocessableEntity, "wrong-port-kind", classFinal},
		{usecases.ErrInvalidRequest, http.StatusBadRequest, "invalid-parameter", classFinal},
		{usecases.ErrUnknownContainer, http.StatusUnprocessableEntity, "unknown-container", classRetryable},
		{usecases.ErrContainerBusy, http.StatusUnprocessableEntity, "container-busy", classRetryable},
		{usecases.ErrContainerAlreadyStored, http.StatusUnprocessableEntity, "container-already-stored", classRetryable},
		{usecases.ErrStorageFull, http.StatusUnprocessableEntity, "storage-full", classRetryable},
		{usecases.ErrAisleUnavailable, http.StatusUnprocessableEntity, "aisle-unavailable", classRetryable},
		{usecases.ErrPortUnreachable, http.StatusUnprocessableEntity, "port-unreachable", classRetryable},
		{usecases.ErrPortNotPresenting, http.StatusConflict, "port-not-presenting", classRetryable},
		{storagejob.ErrNotCancellable, http.StatusConflict, "job-not-cancellable", classRetryable},
		{storagejob.ErrJobEnded, http.StatusConflict, "job-ended", classRetryable},
		{fmt.Errorf("wrapped: %w", errMalformedRequest), http.StatusBadRequest, "bad-request", classFinal},
		{errors.New("boom"), http.StatusInternalServerError, "internal-error", classRetryable},
	}
	for _, c := range cases {
		p := problemFor(c.err)
		if p.status != c.status || p.slug != c.slug || p.class != c.class {
			t.Errorf("%v -> %d %s %s, want %d %s %s", c.err, p.status, p.slug, p.class, c.status, c.slug, c.class)
		}
	}
}

func TestRejectNULBounds(t *testing.T) {
	for _, ok := range []string{"C", "C-0001", "sj-11111111-2222-4333-8444-555555555555"} {
		if err := rejectNUL("f", ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{"", "a\x00b", "a\nb", string(long)} {
		if err := rejectNUL("f", bad); !errors.Is(err, errInvalidParameter) {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := rejectNUL("f", string(long[:128])); err != nil {
		t.Errorf("128 characters refused: %v", err)
	}
}
