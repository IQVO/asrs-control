package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// params reads and checks the named path parameters.
func params(r *http.Request, names ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		v := chi.URLParam(r, n)
		if err := rejectNUL(n, v); err != nil {
			return nil, err
		}
		out[n] = v
	}
	return out, nil
}

func (s *Server) handleGetAsrs(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "asrsId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.Asrs(r.Context(), p["asrsId"])
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAsrs(v))
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "asrsId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body createJobRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, r, err)
		return
	}
	var j *storagejob.Job
	if body.Kind == string(storagejob.Store) {
		j, err = s.Service.RequestStore(r.Context(), usecases.StoreRequest{AsrsID: p["asrsId"], ContainerID: body.ContainerID, PortID: body.PortID, Reference: body.Reference})
	} else {
		j, err = s.Service.RequestRetrieve(r.Context(), usecases.RetrieveRequest{AsrsID: p["asrsId"], ContainerID: body.ContainerID, PortID: body.PortID, Reference: body.Reference})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/asrs/"+p["asrsId"]+"/storage-jobs/"+j.ID())
	writeJSON(w, http.StatusAccepted, toJob(j))
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "asrsId", "storageJobId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	j, err := s.Service.Job(r.Context(), p["asrsId"], p["storageJobId"])
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toJob(j))
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "asrsId", "storageJobId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	j, err := s.Service.Cancel(r.Context(), p["asrsId"], p["storageJobId"])
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if j.Status() == storagejob.CancelRequested {
		status = http.StatusAccepted
	}
	writeJSON(w, status, toJob(j))
}

func (s *Server) handlePickup(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "asrsId", "portId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body pickupRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := rejectNUL("container_id", body.ContainerID); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.Service.ConfirmPickup(r.Context(), p["asrsId"], p["portId"], body.ContainerID); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pickupResponse{AsrsID: p["asrsId"], PortID: p["portId"], ContainerID: body.ContainerID})
}
