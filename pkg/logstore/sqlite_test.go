package logstore_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/piper/piper/pkg/logstore"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE logs (
		id        INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id    TEXT NOT NULL,
		step_name TEXT NOT NULL,
		ts        DATETIME NOT NULL,
		stream    TEXT NOT NULL,
		line      TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteLogStore_AppendAndQuery(t *testing.T) {
	db := openTestDB(t)
	ls := logstore.NewSQLite(db)

	lines := []*logstore.Line{
		{RunID: "r1", StepName: "train", Ts: time.Now(), Stream: "stdout", Line: "epoch 1"},
		{RunID: "r1", StepName: "train", Ts: time.Now(), Stream: "stdout", Line: "epoch 2"},
	}
	if err := ls.Append(lines); err != nil {
		t.Fatal(err)
	}

	got, err := ls.Query("r1", "train", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if got[0].Line != "epoch 1" || got[1].Line != "epoch 2" {
		t.Errorf("unexpected lines: %v", got)
	}
}

func TestSQLiteLogStore_QueryAfterID(t *testing.T) {
	db := openTestDB(t)
	ls := logstore.NewSQLite(db)

	for i := 0; i < 5; i++ {
		_ = ls.Append([]*logstore.Line{
			{RunID: "r1", StepName: "s1", Ts: time.Now(), Stream: "stdout", Line: "line"},
		})
	}

	all, _ := ls.Query("r1", "s1", 0)
	if len(all) != 5 {
		t.Fatalf("expected 5, got %d", len(all))
	}

	// Query only lines after the 3rd
	tail, err := ls.Query("r1", "s1", all[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 lines after id %d, got %d", all[2].ID, len(tail))
	}
}

func TestSQLiteLogStore_EmptyAppend(t *testing.T) {
	db := openTestDB(t)
	ls := logstore.NewSQLite(db)
	if err := ls.Append(nil); err != nil {
		t.Error("empty append should not fail")
	}
}

func TestSQLiteLogStore_QueryDifferentSteps(t *testing.T) {
	db := openTestDB(t)
	ls := logstore.NewSQLite(db)

	_ = ls.Append([]*logstore.Line{
		{RunID: "r1", StepName: "step-a", Ts: time.Now(), Stream: "stdout", Line: "a"},
		{RunID: "r1", StepName: "step-b", Ts: time.Now(), Stream: "stdout", Line: "b"},
	})

	gotA, _ := ls.Query("r1", "step-a", 0)
	gotB, _ := ls.Query("r1", "step-b", 0)
	if len(gotA) != 1 || gotA[0].Line != "a" {
		t.Errorf("step-a: expected [a], got %v", gotA)
	}
	if len(gotB) != 1 || gotB[0].Line != "b" {
		t.Errorf("step-b: expected [b], got %v", gotB)
	}
}
