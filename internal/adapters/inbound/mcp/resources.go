package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// jobURIPrefix and jobURITemplate are the ONE resource template this server offers (Agentic
// Standard v2, AS-3): a storage job, loadable without a tool call.
const (
	jobURIPrefix   = "warehouse://asrs-control/asrs/"
	jobURITemplate = jobURIPrefix + "{asrsId}/storage-jobs/{storageJobId}"
)

// registerResources adds the storage job resource template (a read; resources cannot write).
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: jobURITemplate,
		Name:        "storage-job",
		Description: "One STORE or RETRIEVE job of an AS/RS: container, port, status, failure reason, accepted and ended times.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		asrsID, jobID, ok := parseJobURI(req.Params.URI)
		if !ok {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		j, err := d.Reads.Job(ctx, asrsID, jobID)
		if err != nil {
			return nil, mapError(ctx, err)
		}
		encoded, err := json.Marshal(toJob(j))
		if err != nil {
			return nil, mapError(ctx, err)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: mimeTypeJSON, Text: string(encoded)}}}, nil
	})
}

// parseJobURI reads <asrsId> and <storageJobId> off warehouse://asrs-control/asrs/<a>/storage-jobs/<j>.
func parseJobURI(uri string) (asrsID, jobID string, ok bool) {
	rest, found := strings.CutPrefix(uri, jobURIPrefix)
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "storage-jobs" || parts[0] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}
