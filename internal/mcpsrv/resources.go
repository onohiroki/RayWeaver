package mcpsrv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerResources exposes the manuals, the demo inputs and every produced
// document as MCP resources, so the model can read the owning documentation
// without leaving the protocol (and without a filesystem tool).
func (s *server) registerResources() {
	s.docsDir = findAssetDir("docs")
	s.samplesDir = findAssetDir("samples")

	if s.docsDir != "" {
		s.mcp.AddResource(&mcp.Resource{
			URI:         "rayweave://docs",
			Name:        "docs",
			Title:       "RayWeaver manuals (index)",
			Description: "Index of the manuals: the owning document of every behaviour.",
			MIMEType:    "text/markdown",
		}, s.readDocsIndex)
		s.addDocResources()
	}
	if s.samplesDir != "" {
		s.mcp.AddResource(&mcp.Resource{
			URI:         "rayweave://samples",
			Name:        "samples",
			Title:       "Demo inputs (index)",
			Description: "Index of the demo lens documents and scripts.",
			MIMEType:    "text/markdown",
		}, s.readSamplesIndex)
	}

	s.mcp.AddResource(&mcp.Resource{
		URI:         "rayweave://documents",
		Name:        "documents",
		Title:       "Produced documents (index)",
		Description: "Every document produced in this workspace, newest first, with its command and byte size.",
		MIMEType:    "application/json",
	}, s.readDocumentsIndex)

	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "rayweave://documents/{id}",
		Name:        "document",
		Title:       "A pipeline document",
		Description: "The full YAML document produced by a tool call (the result carries the id).",
		MIMEType:    "application/yaml",
	}, s.readDocument)

	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "rayweave://docs/{+path}",
		Name:        "manual",
		Title:       "A manual page",
		Description: "A file under docs/, e.g. rayweave://docs/optimize.md or rayweave://docs/methods/dls-optimization.md.",
		MIMEType:    "text/markdown",
	}, s.readDocFile)

	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "rayweave://samples/{+path}",
		Name:        "sample",
		Title:       "A demo input",
		Description: "A file under samples/, e.g. rayweave://samples/us2645157.yaml.",
		MIMEType:    "application/yaml",
	}, s.readSampleFile)

	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "rayweave://files/{+path}",
		Name:        "workspace_file",
		Title:       "A workspace file",
		Description: "A file written by a tool (plots, exports) under the MCP workspace.",
	}, s.readWorkspaceFile)

	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "rayweave://jobs/{id}/progress",
		Name:        "job_progress",
		Title:       "Global-search progress log",
		Description: "The JSONL progress stream of an escape/pso job.",
		MIMEType:    "application/x-ndjson",
	}, s.readJobProgress)
}

// addDocResources registers each manual page as a static resource, so the docs
// are discoverable without expanding a template.
func (s *server) addDocResources() {
	_ = filepath.WalkDir(s.docsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(s.docsDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		st, _ := d.Info()
		var size int64
		if st != nil {
			size = st.Size()
		}
		s.mcp.AddResource(&mcp.Resource{
			URI:         "rayweave://docs/" + rel,
			Name:        rel,
			Title:       strings.TrimSuffix(filepath.Base(rel), ".md"),
			Description: "RayWeaver manual: " + rel,
			MIMEType:    "text/markdown",
			Size:        size,
		}, s.readDocFile)
		return nil
	})
}

func (s *server) readDocsIndex(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	var b strings.Builder
	b.WriteString("# RayWeaver manuals\n\n")
	err := filepath.WalkDir(s.docsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(s.docsDir, path)
		if rerr != nil {
			return nil
		}
		fmt.Fprintf(&b, "- rayweave://docs/%s\n", filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, "text/markdown", b.String()), nil
}

func (s *server) readSamplesIndex(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	entries, err := os.ReadDir(s.samplesDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# RayWeaver demo inputs\n\n")
	for _, n := range names {
		fmt.Fprintf(&b, "- rayweave://samples/%s\n", n)
	}
	return textResource(req.Params.URI, "text/markdown", b.String()), nil
}

func (s *server) readDocumentsIndex(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	docs, err := s.store.List()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("[\n")
	for i, d := range docs {
		line := fmt.Sprintf(`  {"id":%q,"uri":%q,"command":%q,"bytes":%d,"created_at":%q}`,
			d.ID, s.store.URI(d.ID), d.Command, d.Bytes, d.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
		if i < len(docs)-1 {
			line += ","
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("]\n")
	return textResource(req.Params.URI, "application/json", b.String()), nil
}

func (s *server) readDocument(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	id := strings.TrimPrefix(req.Params.URI, "rayweave://documents/")
	_, data, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, "application/yaml", string(data)), nil
}

func (s *server) readDocFile(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	rel := strings.TrimPrefix(req.Params.URI, "rayweave://docs/")
	if rel == req.Params.URI {
		rel = ""
	}
	path, err := confinedPath(s.docsDir, rel)
	if err != nil {
		return nil, err
	}
	if path == s.docsDir {
		return s.readDocsIndex(ctx, req)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, "text/markdown", string(data)), nil
}

func (s *server) readSampleFile(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	rel := strings.TrimPrefix(req.Params.URI, "rayweave://samples/")
	path, err := confinedPath(s.samplesDir, rel)
	if err != nil {
		return nil, err
	}
	if path == s.samplesDir {
		return s.readSamplesIndex(ctx, req)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, mimeByExt(path), string(data)), nil
}

func (s *server) readWorkspaceFile(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	rel := strings.TrimPrefix(req.Params.URI, "rayweave://files/")
	path, err := s.store.resolvePath(rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if isTextFile(path) {
		return textResource(req.Params.URI, mimeByExt(path), string(data)), nil
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
		{URI: req.Params.URI, MIMEType: mimeByExt(path), Blob: data},
	}}, nil
}

func (s *server) readJobProgress(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	rest := strings.TrimPrefix(req.Params.URI, "rayweave://jobs/")
	id, ok := strings.CutSuffix(rest, "/progress")
	if !ok || id == "" || strings.ContainsAny(id, `/\`) {
		return nil, fmt.Errorf("invalid job progress URI %q", req.Params.URI)
	}
	path := filepath.Join(s.jobs.root, id, "progress.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return textResource(req.Params.URI, "application/x-ndjson", string(data)), nil
}

// confinedPath resolves rel under root and refuses anything that escapes it.
func confinedPath(root, rel string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("no asset directory is configured")
	}
	if rel == "" {
		return root, nil
	}
	path := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	r, err := filepath.Rel(root, path)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("resource path %q is outside %s", rel, root)
	}
	return path, nil
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
		{URI: uri, MIMEType: mime, Text: text},
	}}
}

func mimeByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".jsonl":
		return "application/x-ndjson"
	case ".csv":
		return "text/csv"
	case ".md":
		return "text/markdown"
	case ".zmx", ".seq", ".len":
		return "text/plain"
	case ".yaml", ".yml":
		return "application/yaml"
	default:
		return "text/plain"
	}
}

func isTextFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}

// findAssetDir locates the repository's docs/ or samples/ directory: the
// explicit root, the working directory (walking up, so the server also works
// from a subdirectory), then next to or above the server executable.
func findAssetDir(kind string) string {
	var cands []string
	if r := os.Getenv("RAYWEAVE_ROOT"); r != "" {
		cands = append(cands, filepath.Join(r, kind))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, walkUp(wd, kind)...)
	}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, walkUp(filepath.Dir(exe), kind)...)
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return ""
}

// walkUp returns base/kind, base/../kind, ... for a few levels.
func walkUp(base, kind string) []string {
	var out []string
	dir := base
	for i := 0; i < 6 && dir != ""; i++ {
		out = append(out, filepath.Join(dir, kind))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}
