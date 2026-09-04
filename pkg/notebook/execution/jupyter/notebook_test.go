package jupyter

import (
	"encoding/json"
	"testing"
)

func TestSourceUnmarshalAcceptsStringOrList(t *testing.T) {
	var s Source
	if err := json.Unmarshal([]byte(`"print(1)\nprint(2)"`), &s); err != nil {
		t.Fatalf("unmarshal string form: %v", err)
	}
	if s.String() != "print(1)\nprint(2)" {
		t.Fatalf("String() = %q, want %q", s.String(), "print(1)\nprint(2)")
	}

	var s2 Source
	if err := json.Unmarshal([]byte(`["print(1)\n", "print(2)"]`), &s2); err != nil {
		t.Fatalf("unmarshal list form: %v", err)
	}
	if s2.String() != "print(1)\nprint(2)" {
		t.Fatalf("String() = %q, want %q", s2.String(), "print(1)\nprint(2)")
	}
}

func TestSourceMarshalRoundTrip(t *testing.T) {
	s := NewSource("a = 1\nb = 2\nprint(a + b)")
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Source
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.String() != s.String() {
		t.Fatalf("round trip = %q, want %q", back.String(), s.String())
	}
}

func TestParseNotebookRejectsNonNotebook(t *testing.T) {
	if _, err := ParseNotebook([]byte(`{"foo": "bar"}`)); err == nil {
		t.Fatal("ParseNotebook accepted a document with no nbformat field")
	}
}

func TestParseNotebookRoundTrip(t *testing.T) {
	raw := []byte(`{
		"nbformat": 4,
		"nbformat_minor": 5,
		"metadata": {"kernelspec": {"name": "python3"}},
		"cells": [
			{"id": "c1", "cell_type": "code", "source": ["print('hi')"], "metadata": {}},
			{"id": "c2", "cell_type": "markdown", "source": ["# title"], "metadata": {}}
		]
	}`)
	nb, err := ParseNotebook(raw)
	if err != nil {
		t.Fatalf("ParseNotebook: %v", err)
	}
	if len(nb.Cells) != 2 {
		t.Fatalf("Cells = %d, want 2", len(nb.Cells))
	}
	idx := nb.CodeCellIndexes()
	if len(idx) != 1 || idx[0] != 0 {
		t.Fatalf("CodeCellIndexes = %v, want [0]", idx)
	}

	out, err := nb.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	nb2, err := ParseNotebook(out)
	if err != nil {
		t.Fatalf("re-parse marshaled notebook: %v", err)
	}
	if len(nb2.Cells) != 2 || nb2.Cells[0].ID != "c1" {
		t.Fatalf("round-tripped notebook lost data: %#v", nb2)
	}
}

// TestMarshalPreservesEmptyOutputsOnCodeCells is a regression test for AN:
// a code cell's "outputs": [] used to vanish on save (encoding/json's
// omitempty treats a zero-length slice the same as nil), so a notebook that
// was valid nbformat when it arrived became invalid nbformat once Piper
// re-serialized it to send to Jupyter — jupyter_server's own parser then
// raised KeyError('outputs') and the save failed with a 500.
func TestMarshalPreservesEmptyOutputsOnCodeCells(t *testing.T) {
	raw := []byte(`{
		"nbformat": 4,
		"nbformat_minor": 5,
		"metadata": {},
		"cells": [
			{"id": "c1", "cell_type": "code", "source": ["1+1"], "metadata": {}, "outputs": []},
			{"id": "c2", "cell_type": "markdown", "source": ["# title"], "metadata": {}}
		]
	}`)
	nb, err := ParseNotebook(raw)
	if err != nil {
		t.Fatalf("ParseNotebook: %v", err)
	}

	out, err := nb.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var asMap struct {
		Cells []map[string]json.RawMessage `json:"cells"`
	}
	if err := json.Unmarshal(out, &asMap); err != nil {
		t.Fatalf("re-parse marshaled notebook as generic JSON: %v", err)
	}
	if len(asMap.Cells) != 2 {
		t.Fatalf("Cells = %d, want 2", len(asMap.Cells))
	}
	if _, ok := asMap.Cells[0]["outputs"]; !ok {
		t.Fatal("code cell's outputs field was dropped by Marshal — nbformat requires it even when empty")
	}
	if string(asMap.Cells[0]["outputs"]) != "[]" {
		t.Fatalf("code cell outputs = %s, want []", asMap.Cells[0]["outputs"])
	}
	if _, ok := asMap.Cells[1]["outputs"]; ok {
		t.Fatal("markdown cell must not carry an outputs field at all")
	}
}

func TestContentHashStableAndSensitiveToChange(t *testing.T) {
	nb := EmptyNotebook()
	nb.AppendCodeCell("c1", "print(1)")
	h1 := nb.ContentHash()
	h2 := nb.ContentHash()
	if h1 == "" {
		t.Fatal("ContentHash returned empty string")
	}
	if h1 != h2 {
		t.Fatalf("ContentHash is not stable across calls: %q != %q", h1, h2)
	}

	nb.AppendCodeCell("c2", "print(2)")
	h3 := nb.ContentHash()
	if h3 == h1 {
		t.Fatal("ContentHash did not change after appending a cell")
	}
}

// TestContentHashIgnoresCellID is the regression for a bug found via live
// testing against a real jupyter_server: nbformat_minor>=5 requires every
// cell to have an `id`, and when the on-disk file doesn't have one,
// jupyter_server mints a fresh *random* id on every independent read
// without persisting it back to disk — confirmed live, two reads of the
// byte-identical file a few hundred milliseconds apart returned different
// ids. Before this fix, ContentHash hashed the ID as received, so the
// conflict check (design doc §6.1 step 11) permanently, spuriously fired
// "content changed" for any such notebook — every "notebook" kind
// execution against an id-less notebook ended StatusConflicted instead of
// StatusSucceeded, even though nothing on disk ever changed. Two
// same-content notebooks differing only in cell ID must hash identically.
func TestContentHashIgnoresCellID(t *testing.T) {
	a := EmptyNotebook()
	a.AppendCodeCell("random-id-from-read-one", "print(1)")
	b := EmptyNotebook()
	b.AppendCodeCell("completely-different-id-from-read-two", "print(1)")

	if a.ContentHash() != b.ContentHash() {
		t.Fatalf("ContentHash differed for cells with identical content but different IDs: %q != %q", a.ContentHash(), b.ContentHash())
	}

	// Still sensitive to an actual content change, with IDs held constant —
	// the fix must not make the hash blind to everything.
	c := EmptyNotebook()
	c.AppendCodeCell("random-id-from-read-one", "print(2)")
	if a.ContentHash() == c.ContentHash() {
		t.Fatal("ContentHash did not change after a real source edit")
	}
}

func TestAppendAndReplaceCodeCell(t *testing.T) {
	nb := EmptyNotebook()
	idx := nb.AppendCodeCell("cell-1", "x = 1")
	if idx != 0 {
		t.Fatalf("AppendCodeCell index = %d, want 0", idx)
	}
	if nb.Cells[0].Source.String() != "x = 1" {
		t.Fatalf("appended source = %q, want %q", nb.Cells[0].Source.String(), "x = 1")
	}

	replacedIdx, err := nb.ReplaceCellSource("cell-1", "x = 2")
	if err != nil {
		t.Fatalf("ReplaceCellSource: %v", err)
	}
	if replacedIdx != 0 {
		t.Fatalf("ReplaceCellSource index = %d, want 0", replacedIdx)
	}
	if nb.Cells[0].Source.String() != "x = 2" {
		t.Fatalf("replaced source = %q, want %q", nb.Cells[0].Source.String(), "x = 2")
	}
	if nb.Cells[0].Outputs != nil {
		t.Fatal("ReplaceCellSource did not clear stale outputs")
	}

	if _, err := nb.ReplaceCellSource("does-not-exist", "y = 1"); err == nil {
		t.Fatal("ReplaceCellSource accepted an unknown cell id")
	}
}

func TestSHA256HexDeterministic(t *testing.T) {
	a := SHA256Hex([]byte("hello"))
	b := SHA256Hex([]byte("hello"))
	c := SHA256Hex([]byte("world"))
	if a != b {
		t.Fatal("SHA256Hex is not deterministic")
	}
	if a == c {
		t.Fatal("SHA256Hex produced the same digest for different input")
	}
}
