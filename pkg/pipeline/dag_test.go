package pipeline

import (
	"testing"
)

func steps(defs ...any) []Step {
	// defs: "name", []string{deps...}, ...
	var result []Step
	for i := 0; i < len(defs); i += 2 {
		name := defs[i].(string)
		var deps []string
		if i+1 < len(defs) {
			deps = defs[i+1].([]string)
		}
		result = append(result, Step{Name: name, DependsOn: deps})
	}
	return result
}

func pipeline1(ss []Step) *Pipeline {
	return &Pipeline{Spec: Spec{Steps: ss}}
}

func TestBuildDAG_linear(t *testing.T) {
	p := pipeline1(steps(
		"a", []string{},
		"b", []string{"a"},
		"c", []string{"b"},
	))
	dag, err := BuildDAG(p)
	if err != nil {
		t.Fatal(err)
	}
	order := dag.Order()
	if len(order) != 3 {
		t.Fatalf("want 3 steps, got %d", len(order))
	}
	if order[0].Name != "a" || order[1].Name != "b" || order[2].Name != "c" {
		t.Errorf("unexpected order: %v", names(order))
	}
}

func TestBuildDAG_parallel(t *testing.T) {
	// a → c, b → c (a and b can run in parallel)
	p := pipeline1(steps(
		"a", []string{},
		"b", []string{},
		"c", []string{"a", "b"},
	))
	dag, err := BuildDAG(p)
	if err != nil {
		t.Fatal(err)
	}
	order := dag.Order()
	if order[2].Name != "c" {
		t.Errorf("c must be last, got %v", names(order))
	}
}

func TestBuildDAG_cycle(t *testing.T) {
	p := pipeline1(steps(
		"a", []string{"b"},
		"b", []string{"a"},
	))
	_, err := BuildDAG(p)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestRunnable(t *testing.T) {
	p := pipeline1(steps(
		"a", []string{},
		"b", []string{"a"},
		"c", []string{"a"},
	))
	dag, _ := BuildDAG(p)

	// 아무것도 완료 안 된 상태 → a만 실행 가능
	runnable := dag.Runnable(map[string]bool{})
	if len(runnable) != 1 || runnable[0].Name != "a" {
		t.Errorf("want [a], got %v", names(runnable))
	}

	// a 완료 → b, c 실행 가능
	runnable = dag.Runnable(map[string]bool{"a": true})
	if len(runnable) != 2 {
		t.Errorf("want [b,c], got %v", names(runnable))
	}
}

func TestRunnable_allDone(t *testing.T) {
	p := pipeline1(steps("a", []string{}, "b", []string{"a"}))
	dag, _ := BuildDAG(p)

	runnable := dag.Runnable(map[string]bool{"a": true, "b": true})
	if len(runnable) != 0 {
		t.Errorf("want empty, got %v", names(runnable))
	}
}

func names(ss []*Step) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}
