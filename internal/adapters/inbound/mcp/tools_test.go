package mcp_test

import (
	"strings"
	"testing"
)

type jobOut struct {
	StorageJobID string `json:"storage_job_id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	ContainerID  string `json:"container_id"`
}

func TestListAsrsNamesTheLayout(t *testing.T) {
	h := newHarness(t)
	var out struct {
		AsrsIDs []string `json:"asrs_ids"`
	}
	h.structured(h.call("list_asrs", `{}`), &out)
	if len(out.AsrsIDs) != 1 || out.AsrsIDs[0] != "ASRS1" {
		t.Fatalf("list_asrs = %v", out.AsrsIDs)
	}
}

func TestGetAsrsStateAndCapacity(t *testing.T) {
	h := newHarness(t)
	h.store("C1", "IN1")
	var state struct {
		AsrsID string `json:"asrs_id"`
		Aisles []struct {
			Aisle    int  `json:"aisle"`
			Busy     bool `json:"busy"`
			OpenJobs int  `json:"open_jobs"`
		} `json:"aisles"`
		Ports []struct {
			PortID string `json:"port_id"`
		} `json:"ports"`
		RecommendedInputPortID string `json:"recommended_input_port_id"`
		Capacity               struct {
			OpenJobs int `json:"open_jobs"`
		} `json:"capacity"`
	}
	h.structured(h.call("get_asrs_state", `{"asrsId":"ASRS1"}`), &state)
	if state.AsrsID != "ASRS1" || len(state.Aisles) != 2 || len(state.Ports) != 4 || state.Capacity.OpenJobs != 1 ||
		state.RecommendedInputPortID != "IN2" {
		t.Fatalf("get_asrs_state = %+v (IN2: aisle 2 is the least loaded)", state)
	}
	var capacity struct {
		AsrsID         string `json:"asrs_id"`
		UnitsTotal     int    `json:"units_total"`
		PositionsTotal int    `json:"positions_total"`
		OpenJobs       int    `json:"open_jobs"`
	}
	h.structured(h.call("get_asrs_capacity", `{"asrsId":"ASRS1"}`), &capacity)
	if capacity.AsrsID != "ASRS1" || capacity.UnitsTotal != 2 || capacity.PositionsTotal != 8 || capacity.OpenJobs != 1 {
		t.Fatalf("get_asrs_capacity = %+v", capacity)
	}
}

func TestGetStorageJob(t *testing.T) {
	h := newHarness(t)
	id := h.store("C1", "IN1")
	var job jobOut
	h.structured(h.call("get_storage_job", `{"asrsId":"ASRS1","storageJobId":"`+id+`"}`), &job)
	if job.StorageJobID != id || job.Kind != "STORE" || job.Status != "DISPATCHED" || job.ContainerID != "C1" {
		t.Fatalf("get_storage_job = %+v", job)
	}
}

func TestReadsOfUnknownThingsAreSluggedToolErrors(t *testing.T) {
	h := newHarness(t)
	cases := map[string][2]string{
		"unknown AS/RS state":    {"get_asrs_state", `{"asrsId":"NOPE"}`},
		"unknown AS/RS capacity": {"get_asrs_capacity", `{"asrsId":"NOPE"}`},
		"unknown AS/RS jobs":     {"list_storage_jobs", `{"asrsId":"NOPE"}`},
		"unknown job":            {"get_storage_job", `{"asrsId":"ASRS1","storageJobId":"sj-none"}`},
	}
	want := map[string]string{"unknown job": "storage-job-not-found"}
	for name, c := range cases {
		slug := want[name]
		if slug == "" {
			slug = "asrs-not-found"
		}
		if got := errorText(h.call(c[0], c[1])); !strings.Contains(got, slug) {
			t.Errorf("%s: error %q, want the %s slug", name, got, slug)
		}
	}
}

func TestListStorageJobsPagesNewestFirstWithACursor(t *testing.T) {
	h := newHarness(t)
	first, second := h.store("C1", "IN1"), h.store("C2", "IN2")
	var page1 struct {
		Items      []jobOut `json:"items"`
		NextCursor string   `json:"nextCursor"`
	}
	h.structured(h.call("list_storage_jobs", `{"asrsId":"ASRS1","limit":1}`), &page1)
	if len(page1.Items) != 1 || page1.Items[0].StorageJobID != second || page1.NextCursor == "" {
		t.Fatalf("page 1 = %+v, want the newest job (%s) and a nextCursor", page1, second)
	}
	var page2 struct {
		Items      []jobOut `json:"items"`
		NextCursor string   `json:"nextCursor"`
	}
	h.structured(h.call("list_storage_jobs", `{"asrsId":"ASRS1","limit":1,"cursor":"`+page1.NextCursor+`"}`), &page2)
	if len(page2.Items) != 1 || page2.Items[0].StorageJobID != first || page2.NextCursor != "" {
		t.Fatalf("page 2 = %+v, want only %s and no further page", page2, first)
	}
}

func TestListStorageJobsFiltersAndRefusesBadInput(t *testing.T) {
	h := newHarness(t)
	h.store("C1", "IN1")
	var none struct {
		Items []jobOut `json:"items"`
	}
	h.structured(h.call("list_storage_jobs", `{"asrsId":"ASRS1","status":"DONE"}`), &none)
	if len(none.Items) != 0 {
		t.Fatalf("DONE filter = %+v, want no item", none.Items)
	}
	for _, args := range []string{`{"asrsId":"ASRS1","cursor":"abc"}`, `{"asrsId":"ASRS1","cursor":"-4"}`,
		`{"asrsId":"ASRS1","status":"LOST"}`, `{"asrsId":"ASRS1","limit":101}`} {
		if got := errorText(h.call("list_storage_jobs", args)); !strings.Contains(got, "invalid-parameter") {
			t.Errorf("%s answered %q, want an invalid-parameter tool error", args, got)
		}
	}
}
