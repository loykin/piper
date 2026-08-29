// Package jupyter implements the low-level Jupyter Server REST/WebSocket
// protocol used by pkg/notebook/execution's NotebookGateway: Contents,
// Sessions, Kernels REST calls (client.go), kernel channel WebSocket
// messaging (channels.go), and .ipynb parse/serialize plus Jupyter
// message -> cell output mapping (this file).
//
// This package knows nothing about Piper's domain model (KernelSession,
// NotebookExecution) or persistence — it only speaks Jupyter's wire
// protocol. See docs/jupyter-mcp-execution.md §4.1 for the package
// boundary this mirrors.
package jupyter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Source is an ipynb "source" field, which the Jupyter format allows to be
// encoded as either a single string or a list of lines (each element
// typically ending in "\n" except the last). Piper always re-serializes as
// the list form, matching what `nbformat` itself writes.
type Source []string

func (s Source) String() string {
	var b bytes.Buffer
	for _, line := range s {
		b.WriteString(line)
	}
	return b.String()
}

// NewSource splits code into the ipynb multi-line source form.
func NewSource(code string) Source {
	if code == "" {
		return Source{}
	}
	lines := splitKeepingNewlines(code)
	return Source(lines)
}

func splitKeepingNewlines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func (s *Source) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*s = NewSource(asString)
		return nil
	}
	var asLines []string
	if err := json.Unmarshal(data, &asLines); err != nil {
		return fmt.Errorf("jupyter: source must be a string or []string: %w", err)
	}
	*s = asLines
	return nil
}

func (s Source) MarshalJSON() ([]byte, error) {
	if s == nil {
		return json.Marshal([]string{})
	}
	return json.Marshal([]string(s))
}

// Output is one ipynb cell output entry. All of the fields Jupyter's
// nbformat v4 defines across the four output_type variants are kept on one
// struct (matching how upstream nbformat itself is commonly modeled in Go)
// rather than a sum type, since round-tripping through JSON is simplest
// this way; MarshalJSON below drops fields that don't apply to the actual
// output_type so the serialized form still matches nbformat.
type Output struct {
	OutputType     string                     `json:"output_type"`
	Name           string                     `json:"name,omitempty"`            // stream
	Text           Source                     `json:"text,omitempty"`            // stream
	Data           map[string]json.RawMessage `json:"data,omitempty"`            // display_data, execute_result
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`        // display_data, execute_result
	ExecutionCount *int                       `json:"execution_count,omitempty"` // execute_result
	Ename          string                     `json:"ename,omitempty"`           // error
	Evalue         string                     `json:"evalue,omitempty"`          // error
	Traceback      []string                   `json:"traceback,omitempty"`       // error
}

// Cell is one ipynb cell. Only "code" cells are executed; other cell types
// round-trip unchanged.
type Cell struct {
	ID             string                     `json:"id,omitempty"`
	CellType       string                     `json:"cell_type"`
	Source         Source                     `json:"source"`
	Metadata       map[string]json.RawMessage `json:"metadata"`
	Outputs        []Output                   `json:"outputs,omitempty"`
	ExecutionCount *int                       `json:"execution_count,omitempty"`
}

const CellTypeCode = "code"

// Notebook is a parsed .ipynb document. Only the fields Piper needs to read
// or mutate are modeled explicitly; unknown top-level metadata keys are
// preserved via RawMetadata so a round-trip save doesn't drop kernelspec /
// language_info the way a human's Jupyter session set them.
type Notebook struct {
	NBFormat      int                        `json:"nbformat"`
	NBFormatMinor int                        `json:"nbformat_minor"`
	Metadata      map[string]json.RawMessage `json:"metadata"`
	Cells         []Cell                     `json:"cells"`
}

// EmptyNotebook returns a minimal valid nbformat v4 document, used when
// create_if_missing is set for a cell execution against a path that doesn't
// exist yet (docs/jupyter-mcp-execution.md §6.2).
func EmptyNotebook() *Notebook {
	return &Notebook{
		NBFormat:      4,
		NBFormatMinor: 5,
		Metadata:      map[string]json.RawMessage{},
		Cells:         []Cell{},
	}
}

// ParseNotebook parses raw .ipynb JSON bytes.
func ParseNotebook(raw []byte) (*Notebook, error) {
	var nb Notebook
	if err := json.Unmarshal(raw, &nb); err != nil {
		return nil, fmt.Errorf("jupyter: parse notebook: %w", err)
	}
	if nb.NBFormat == 0 {
		return nil, fmt.Errorf("jupyter: not a valid notebook document (missing nbformat)")
	}
	return &nb, nil
}

// Marshal serializes the notebook back to canonical ipynb JSON.
func (n *Notebook) Marshal() ([]byte, error) {
	return json.MarshalIndent(n, "", " ")
}

// ContentHash returns the sha256 hex digest of the notebook's canonical
// JSON encoding, with every cell's ID field zeroed out first — see below
// for why this must not hash the ID field as read. Used both for
// NotebookExecution.SourceSHA256/BaseContentHash bookkeeping and for the
// conflict check in docs/jupyter-mcp-execution.md §6.1 step 11: comparing a
// freshly-read original's hash against the hash recorded when the
// execution started.
//
// Confirmed live against a real jupyter_server: nbformat_minor>=5 requires
// every cell to have an `id`, and when the on-disk file doesn't have one
// (nbformat 4.5+ became the default only fairly recently — plenty of real
// notebooks predate it, and it's an easy field for an MCP client
// synthesizing a notebook to omit), jupyter_server's FileContentsManager
// mints a fresh random one on every independent read *without persisting
// it back to disk* — two reads of the byte-identical on-disk file a few
// hundred milliseconds apart returned different ids in testing. Hashing
// the ID as received would make the conflict check permanently, spuriously
// fire "content changed" for any such notebook even when nothing on disk
// ever changed — not a rare edge case for the AI-generated-notebook use
// case this whole feature exists for. A real, persisted ID (one already
// written to disk by a prior save, human or Piper) does round-trip
// identically across reads and would be safe to hash, but there is no way
// to tell that apart from a same-request freshly-minted one by the time
// Piper receives the JSON — so this excludes IDs from the hash
// unconditionally, accepting the (comparatively harmless) tradeoff that a
// cell-reorder-with-no-content-change edit made purely through ID
// reassignment won't be detected as a conflict.
func (n *Notebook) ContentHash() string {
	normalized := *n
	normalized.Cells = make([]Cell, len(n.Cells))
	for i, cell := range n.Cells {
		cell.ID = ""
		normalized.Cells[i] = cell
	}
	raw, err := normalized.Marshal()
	if err != nil {
		return ""
	}
	return SHA256Hex(raw)
}

// SHA256Hex returns the sha256 hex digest of data. Used for both notebook
// content hashes and per-cell source hashes — per design doc §5.3, Piper
// stores code SHA-256, never raw source, in audit records.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CodeCellIndexes returns the indexes (into Cells) of every code cell, in
// document order — the execution order for a full "notebook" kind run.
func (n *Notebook) CodeCellIndexes() []int {
	var out []int
	for i, c := range n.Cells {
		if c.CellType == CellTypeCode {
			out = append(out, i)
		}
	}
	return out
}

// AppendCodeCell adds a new code cell at the end of the document (the
// "append" cell-edit mode, §6.2) and returns its index.
func (n *Notebook) AppendCodeCell(id, code string) int {
	n.Cells = append(n.Cells, Cell{
		ID:       id,
		CellType: CellTypeCode,
		Source:   NewSource(code),
		Metadata: map[string]json.RawMessage{},
	})
	return len(n.Cells) - 1
}

// ReplaceCellSource finds the code cell with the given stable cellID and
// replaces its source (the "replace" cell-edit mode, §6.2). Cell index is
// deliberately not accepted as an identifier — see the design doc's
// rationale: a human inserting cells in the Jupyter UI shifts what an index
// means, but a cell's `id` (nbformat 4.5+) is stable.
func (n *Notebook) ReplaceCellSource(cellID, code string) (int, error) {
	for i := range n.Cells {
		if n.Cells[i].ID == cellID && n.Cells[i].CellType == CellTypeCode {
			n.Cells[i].Source = NewSource(code)
			n.Cells[i].Outputs = nil
			n.Cells[i].ExecutionCount = nil
			return i, nil
		}
	}
	return -1, fmt.Errorf("jupyter: no code cell with id %q", cellID)
}
