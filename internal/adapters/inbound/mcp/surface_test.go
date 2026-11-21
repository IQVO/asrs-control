package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The surface beyond tools (Agentic Standard v2, AS-3): ONE resource template (a storage job) and
// ONE prompt (this context's operator SOP).

func TestResourceTemplateIsAdvertised(t *testing.T) {
	listed, err := newHarness(t).session.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.ResourceTemplates) != 1 || listed.ResourceTemplates[0].URITemplate != "warehouse://asrs-control/asrs/{asrsId}/storage-jobs/{storageJobId}" {
		t.Fatalf("resource templates = %+v", listed.ResourceTemplates)
	}
}

func TestResourceReadsAStorageJob(t *testing.T) {
	h := newHarness(t)
	id := h.store("C1", "IN1")
	res, err := h.session.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "warehouse://asrs-control/asrs/ASRS1/storage-jobs/" + id})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if len(res.Contents) != 1 || json.Unmarshal([]byte(res.Contents[0].Text), &doc) != nil || doc["storage_job_id"] != id || doc["kind"] != "STORE" {
		t.Fatalf("resource = %+v", res.Contents)
	}
}

func TestResourceOfAnUnknownOrMalformedJobIsAnError(t *testing.T) {
	h := newHarness(t)
	for _, uri := range []string{"warehouse://asrs-control/asrs/ASRS1/storage-jobs/sj-none",
		"warehouse://asrs-control/asrs/NOPE/storage-jobs/sj-1", "warehouse://asrs-control/asrs/ASRS1/jobs/sj-1"} {
		if _, err := h.session.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read of %s must error", uri)
		}
	}
}

func TestPromptHandsOutTheSOP(t *testing.T) {
	res, err := newHarness(t).session.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "review_storage_job",
		Arguments: map[string]string{"asrsId": "ASRS1", "storageJobId": "sj-0001"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res.Messages)
	for _, want := range []string{"sj-0001", "ASRS1", "get_storage_job", "get_asrs_state", "Escalate"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the prompt does not carry %q", want)
		}
	}
}
