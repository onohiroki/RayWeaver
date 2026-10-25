package mcpsrv

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DocMeta describes one stored pipeline document. The id is the first 12 hex
// digits of the document's SHA-256: the same input and the same settings
// produce the same id, which makes a run reproducible by construction.
type DocMeta struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Parent    string    `json:"parent,omitempty"`
	Bytes     int       `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// Store keeps produced documents in <workspace>/documents and an append-only
// index. It is a cache: deleting the workspace loses handles but nothing else,
// because every document is also returned as a resource URI.
type Store struct {
	root      string
	docs      string
	maxInline int
	mu        sync.Mutex
}

var docIDPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

// URI is the MCP resource URI of a stored document.
func (s *Store) URI(id string) string { return "rayweave://documents/" + id }

// Path is the on-disk path of a stored document.
func (s *Store) Path(id string) string { return filepath.Join(s.docs, id+".yaml") }

// Put stores data under its content hash and returns its metadata. The parent
// id records which document the call was chained from.
func (s *Store) Put(command string, data []byte, parent string) (DocMeta, error) {
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:6])
	path := s.Path(id)
	meta := DocMeta{ID: id, Command: command, Parent: parent, Bytes: len(data), CreatedAt: time.Now().UTC()}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return meta, nil // content-addressed: identical output already stored
	}
	tmp, err := os.CreateTemp(s.docs, ".put-*.yaml")
	if err != nil {
		return meta, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return meta, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return meta, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return meta, err
	}
	if err := s.appendIndex(meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func (s *Store) appendIndex(meta DocMeta) error {
	f, err := os.OpenFile(filepath.Join(s.root, "documents", "index.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Get returns the stored document and its metadata.
func (s *Store) Get(id string) (DocMeta, []byte, error) {
	if !docIDPattern.MatchString(id) {
		return DocMeta{}, nil, fmt.Errorf("invalid document id %q (expected 12 lowercase hex digits)", id)
	}
	data, err := os.ReadFile(s.Path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return DocMeta{}, nil, fmt.Errorf("document %s is not in the workspace (pass the document inline, or re-run the producing tool)", id)
		}
		return DocMeta{}, nil, err
	}
	return DocMeta{ID: id, Bytes: len(data)}, data, nil
}

// Resolve returns the stdin bytes for a tool call and the parent document id.
// Exactly one source must be given: inline document, document id or path.
func (s *Store) Resolve(in DocInput) ([]byte, string, error) {
	n := 0
	for _, v := range []string{in.Document, in.DocumentID, in.Path} {
		if v != "" {
			n++
		}
	}
	switch {
	case n == 0:
		return nil, "", fmt.Errorf("no input document: pass document, document_id or path")
	case n > 1:
		return nil, "", fmt.Errorf("give exactly one of document, document_id or path")
	}
	switch {
	case in.Document != "":
		if len(in.Document) > s.maxInline {
			return nil, "", fmt.Errorf("inline document is %d bytes, over the %d byte limit; write it under the workspace and pass path, or chain with document_id",
				len(in.Document), s.maxInline)
		}
		return []byte(in.Document), "", nil
	case in.DocumentID != "":
		_, data, err := s.Get(in.DocumentID)
		return data, in.DocumentID, err
	default:
		p, err := s.resolvePath(in.Path)
		if err != nil {
			return nil, "", err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, "", fmt.Errorf("path %s: %w", in.Path, err)
		}
		return data, "", nil
	}
}

// resolvePath confines a tool's path argument to the workspace.
func (s *Store) resolvePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	path := p
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.root, path)
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the MCP workspace %s", p, s.root)
	}
	return path, nil
}

// List returns the indexed documents, newest first. A missing index is an empty
// list: the documents themselves are still readable by id.
func (s *Store) List() ([]DocMeta, error) {
	f, err := os.Open(filepath.Join(s.docs, "index.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []DocMeta
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var m DocMeta
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// WriteFile writes a tool artefact under the workspace, creating its directory.
func (s *Store) WriteFile(rel string, data []byte) (string, error) {
	path, err := s.resolvePath(rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}
