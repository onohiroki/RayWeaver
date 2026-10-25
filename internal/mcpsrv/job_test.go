package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// shortEscape returns the escape demo with a budget a test can finish.
func shortEscape(t *testing.T, cycles string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(srcdir(t), "..", "..", "samples", "escape-demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	doc = strings.Replace(doc, "max_cycles: 4", "max_cycles: "+cycles, 1)
	doc = strings.Replace(doc, "global_workers: 3", "global_workers: 1", 1)
	return doc
}

func TestEscapeJobLifecycle(t *testing.T) {
	session := testSession(t)
	ctx := context.Background()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "escape",
		Arguments: map[string]any{"document": shortEscape(t, "1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("escape did not start: %v", res.Content)
	}
	id, _ := res.StructuredContent.(map[string]any)["id"].(string)
	if !strings.HasPrefix(id, "job-") {
		t.Fatalf("job id = %q", id)
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		st, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "job_status",
			Arguments: map[string]any{"id": id},
		})
		if err != nil {
			t.Fatal(err)
		}
		if st.IsError {
			t.Fatalf("job_status failed: %v", st.Content)
		}
		state := st.StructuredContent.(map[string]any)["state"].(string)
		if state != "running" {
			if state != "finished" {
				t.Fatalf("job ended in state %q", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("escape job did not finish in time; status=%v", st.StructuredContent)
		}
		time.Sleep(500 * time.Millisecond)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "job_result",
		Arguments: map[string]any{"id": id},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("job_result failed: %v", result.Content)
	}
	out := result.StructuredContent.(map[string]any)
	docID, _ := out["document_id"].(string)
	if len(docID) != 12 {
		t.Fatalf("document_id = %q", docID)
	}
	summary, _ := out["summary"].(map[string]any)
	if _, ok := summary["best_index"]; !ok {
		t.Errorf("summary has no best_index: %v", keysOf(summary))
	}
	rr, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "rayweave://documents/" + docID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.Contents[0].Text, "global_result") {
		t.Error("the escape document has no global_result")
	}

	list, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "job_list", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := list.StructuredContent.(map[string]any)["jobs"].([]any)
	if len(jobs) != 1 {
		t.Errorf("job_list returned %d jobs, want 1", len(jobs))
	}
}

func TestJobStopKeepsTheDocument(t *testing.T) {
	session := testSession(t)
	ctx := context.Background()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "escape",
		Arguments: map[string]any{"document": shortEscape(t, "1000")},
	})
	if err != nil || res.IsError {
		t.Fatalf("escape did not start: %v %v", err, res)
	}
	id := res.StructuredContent.(map[string]any)["id"].(string)

	// Let it get going, then stop it with the two-stage SIGTERM.
	time.Sleep(1500 * time.Millisecond)
	stopped, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "job_stop",
		Arguments: map[string]any{"id": id, "grace_seconds": 1, "wait_seconds": 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	if stopped.IsError {
		t.Fatalf("job_stop failed: %v", stopped.Content)
	}
	state := stopped.StructuredContent.(map[string]any)["state"].(string)
	if state == "running" {
		t.Fatalf("job is still running after job_stop: %v", stopped.Content)
	}
	// The document must survive the stop (that is why SIGKILL is never used).
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "job_result",
		Arguments: map[string]any{"id": id},
	}); err != nil {
		t.Fatalf("job_result after stop: %v", err)
	}
}
