package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binPath != "" {
		os.RemoveAll(filepath.Dir(binPath))
	}
	os.Exit(code)
}

// rayweaveBinary builds the CLI once for the end-to-end tests.
func rayweaveBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rayweave-mcp-test-")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "rayweave")
		cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/rayweave")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

// testSession starts the MCP server over an in-memory transport.
func testSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return testSessionOpts(t, nil)
}

func testSessionOpts(t *testing.T, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Binary = rayweaveBinary(t)
	cfg.Workspace = t.TempDir()
	cfg.Tools = "all"
	cfg.MaxJobs = 1
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, opts)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func sampleDocument(t *testing.T) string {
	t.Helper()
	path := filepath.Join(srcdir(t), "..", "..", "samples", "us2645157.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func srcdir(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func TestToolSchemaFlattensEmbeddedInputs(t *testing.T) {
	session := testSession(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"paraxial", "chief", "trace", "optimize", "escape"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("tool %q is not registered", name)
		}
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: input schema is %T, want a JSON object", name, tool.InputSchema)
		}
		props, _ := schema["properties"].(map[string]any)
		for _, want := range []string{"document", "document_id", "path", "config"} {
			if _, ok := props[want]; !ok {
				t.Errorf("%s: input schema has no %q property (embedded struct not flattened): %v", name, want, keysOf(props))
			}
		}
	}
	if _, ok := byName["list"]; !ok {
		t.Error("list tool is missing")
	}
	if _, ok := byName["job_status"]; !ok {
		t.Error("job_status tool is missing")
	}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestParaxialEndToEnd(t *testing.T) {
	session := testSession(t)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "paraxial",
		Arguments: map[string]any{
			"document": sampleDocument(t),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("paraxial failed: %v", res.Content)
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content is %T", res.StructuredContent)
	}
	id, _ := out["document_id"].(string)
	if len(id) != 12 {
		t.Fatalf("document_id = %q, want 12 hex digits", id)
	}
	if out["summary"] == nil {
		t.Error("no summary in the structured result")
	}
	// The full document is reachable as a resource.
	rr, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "rayweave://documents/" + id})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Contents) == 0 || !strings.Contains(rr.Contents[0].Text, "paraxial_result") {
		t.Errorf("document resource does not carry paraxial_result: %q", firstN(rr.Contents[0].Text, 200))
	}
}

func TestPipelineChiefTrace(t *testing.T) {
	session := testSession(t)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "pipeline",
		Arguments: map[string]any{
			"document": sampleDocument(t),
			"stages": []any{
				map[string]any{"command": "chief"},
				map[string]any{"command": "trace"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("pipeline failed: %v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	stages, _ := out["stages"].([]any)
	if len(stages) != 2 {
		t.Fatalf("stages = %v, want 2", out["stages"])
	}
	id, _ := out["document_id"].(string)
	rr, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "rayweave://documents/" + id})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.Contents[0].Text, "results:") {
		t.Error("the final document has no trace results")
	}
}

func TestPipelineRejectsFileWritingFlags(t *testing.T) {
	session := testSession(t)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "pipeline",
		Arguments: map[string]any{
			"document": sampleDocument(t),
			"stages": []any{
				map[string]any{"command": "plot", "args": []any{"-o", "x.svg"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("plot in a pipeline must be rejected")
	}
}

func TestListAndQuery(t *testing.T) {
	session := testSession(t)
	ctx := context.Background()
	chief, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "chief",
		Arguments: map[string]any{"document": sampleDocument(t)},
	})
	if err != nil || chief.IsError {
		t.Fatalf("chief failed: %v %v", err, chief)
	}
	docID := chief.StructuredContent.(map[string]any)["document_id"].(string)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list",
		Arguments: map[string]any{"document_id": docID, "targets": []any{"surfaces"}},
	})
	if err != nil || res.IsError {
		t.Fatalf("list failed: %v %v", err, res.Content)
	}

	q, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "query",
		Arguments: map[string]any{"document_id": docID, "selector": "chief_rays[0].spot_stats.rms_r"},
	})
	if err != nil || q.IsError {
		t.Fatalf("query failed: %v %v", err, q.Content)
	}
}

func TestUnknownDocumentID(t *testing.T) {
	session := testSession(t)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "paraxial",
		Arguments: map[string]any{"document_id": "000000000000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("an unknown document id must be an error")
	}
}

func TestDocumentsAndDocsResources(t *testing.T) {
	session := testSession(t)
	ctx := context.Background()
	rr, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "rayweave://docs/optimize.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.Contents[0].Text, "optimize") {
		t.Error("docs/optimize.md is not served")
	}
	list, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var docs int
	for _, r := range list.Resources {
		if strings.HasPrefix(r.URI, "rayweave://docs/") {
			docs++
		}
	}
	if docs < 10 {
		t.Errorf("only %d manuals are registered", docs)
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func TestOptimizeSummaryAndDiagnostics(t *testing.T) {
	session := testSession(t)
	data, err := os.ReadFile(filepath.Join(srcdir(t), "..", "..", "samples", "optimize-demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(string(data), "  method: dls\n", "  method: dls\n  max_iter: 1\n", 1)
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "optimize",
		Arguments: map[string]any{"document": doc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("optimize failed: %v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	summary, _ := out["summary"].(map[string]any)
	if summary == nil {
		t.Fatal("no summary")
	}
	if _, ok := summary["terms"]; !ok {
		t.Errorf("merit summary has no terms: %v", keysOf(summary))
	}
}

func TestOptimizeSendsProgress(t *testing.T) {
	var mu sync.Mutex
	var messages []string
	session := testSessionOpts(t, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			messages = append(messages, req.Params.Message)
			mu.Unlock()
		},
	})
	data, err := os.ReadFile(filepath.Join(srcdir(t), "..", "..", "samples", "optimize-demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(string(data), "  method: dls\n", "  method: dls\n  max_iter: 2\n", 1)
	params := &mcp.CallToolParams{
		Name:      "optimize",
		Arguments: map[string]any{"document": doc},
	}
	params.SetProgressToken("optimize-test")
	res, err := session.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("optimize failed: %v", res.Content)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) == 0 {
		t.Error("optimize sent no progress notification despite the progress token")
	}
	for _, m := range messages {
		if !strings.Contains(m, "iteration") && !strings.Contains(m, "merit") {
			t.Errorf("progress message %q does not describe the solve", m)
		}
	}
}

func TestPlotReturnsAnImage(t *testing.T) {
	session := testSession(t)
	ctx := context.Background()
	piped, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "pipeline",
		Arguments: map[string]any{
			"document": sampleDocument(t),
			"stages":   []any{map[string]any{"command": "chief"}, map[string]any{"command": "trace"}},
		},
	})
	if err != nil || piped.IsError {
		t.Fatalf("pipeline failed: %v %v", err, piped)
	}
	docID := piped.StructuredContent.(map[string]any)["document_id"].(string)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "plot",
		Arguments: map[string]any{"document_id": docID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("plot failed: %v", res.Content)
	}
	var images int
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok {
			if img.MIMEType != "image/png" || len(img.Data) < 100 {
				t.Errorf("bad image content: %s, %d bytes", img.MIMEType, len(img.Data))
			}
			images++
		}
	}
	if images != 1 {
		t.Errorf("plot returned %d images, want 1: %v", images, res.Content)
	}
}

func TestScaleAndTMMStages(t *testing.T) {
	session := testSession(t)
	ctx := context.Background()
	scaled, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "scale",
		Arguments: map[string]any{"document": sampleDocument(t), "efl": 50.0},
	})
	if err != nil || scaled.IsError {
		t.Fatalf("scale failed: %v %v", err, scaled)
	}
	id := scaled.StructuredContent.(map[string]any)["document_id"].(string)
	if id == "" {
		t.Error("scale returned no document")
	}
	coat, err := os.ReadFile(filepath.Join(srcdir(t), "..", "..", "samples", "ar-coating.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tmm, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tmm",
		Arguments: map[string]any{"document": string(coat)},
	})
	if err != nil || tmm.IsError {
		t.Fatalf("tmm failed: %v %v", err, tmm)
	}
}

func TestPSFAndWavefrontSummaries(t *testing.T) {
	session := testSession(t)
	data, err := os.ReadFile(filepath.Join(srcdir(t), "..", "..", "samples", "psf-mtf-demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"psf", "wavefront"} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      name,
			Arguments: map[string]any{"document": string(data)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("%s failed: %v", name, res.Content)
		}
		out := res.StructuredContent.(map[string]any)
		raw, err := json.Marshal(out["summary"])
		if err != nil {
			t.Fatalf("%s summary is not JSON: %v", name, err)
		}
		if len(raw) > 70<<10 {
			t.Errorf("%s summary is %d bytes, over the compaction budget", name, len(raw))
		}
	}
}

func TestExportWritesAWorkspaceFileAndImportsBack(t *testing.T) {
	session := testSession(t)
	ctx := context.Background()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "export",
		Arguments: map[string]any{
			"document": sampleDocument(t),
			"output":   "out.zmx",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("export failed: %v", res.Content)
	}
	out := res.StructuredContent.(map[string]any)
	summary, _ := out["summary"].(map[string]any)
	if summary["format"] != "zemax" {
		t.Errorf("format = %v, want zemax (inferred from the .zmx extension)", summary["format"])
	}
	rr, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "rayweave://files/out.zmx"})
	if err != nil {
		t.Fatal(err)
	}
	text := rr.Contents[0].Text
	if !strings.Contains(text, "VERS") || !strings.Contains(text, "SURF") {
		t.Errorf("exported file is not ZEMAX text: %q", firstN(text, 120))
	}
	imported, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "import",
		Arguments: map[string]any{
			"content":  text,
			"format":   "zemax",
			"no_chief": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if imported.IsError {
		t.Fatalf("import of the exported file failed: %v", imported.Content)
	}
	if id := imported.StructuredContent.(map[string]any)["document_id"].(string); len(id) != 12 {
		t.Errorf("imported document id = %q", id)
	}
}
