package pipeline

import "fmt"

// DAG은 step 실행 순서를 결정
type DAG struct {
	steps map[string]*Step
	order []string // 위상 정렬된 실행 순서
}

func BuildDAG(p *Pipeline) (*DAG, error) {
	dag := &DAG{
		steps: make(map[string]*Step),
	}
	for i := range p.Spec.Steps {
		s := &p.Spec.Steps[i]
		dag.steps[s.Name] = s
	}

	order, err := topoSort(p.Spec.Steps)
	if err != nil {
		return nil, err
	}
	dag.order = order
	return dag, nil
}

// Order는 위상 정렬된 step 순서를 반환
func (d *DAG) Order() []*Step {
	result := make([]*Step, len(d.order))
	for i, name := range d.order {
		result[i] = d.steps[name]
	}
	return result
}

// Runnable은 완료된 step 목록을 받아 지금 실행 가능한 step을 반환
func (d *DAG) Runnable(done map[string]bool) []*Step {
	var result []*Step
	for _, name := range d.order {
		if done[name] {
			continue
		}
		step := d.steps[name]
		if d.depsReady(step, done) {
			result = append(result, step)
		}
	}
	return result
}

func (d *DAG) depsReady(s *Step, done map[string]bool) bool {
	for _, dep := range s.DependsOn {
		if !done[dep] {
			return false
		}
	}
	return true
}

func topoSort(steps []Step) ([]string, error) {
	inDegree := make(map[string]int)
	adj := make(map[string][]string)

	for _, s := range steps {
		if _, ok := inDegree[s.Name]; !ok {
			inDegree[s.Name] = 0
		}
		for _, dep := range s.DependsOn {
			adj[dep] = append(adj[dep], s.Name)
			inDegree[s.Name]++
		}
	}

	var queue []string
	for _, s := range steps {
		if inDegree[s.Name] == 0 {
			queue = append(queue, s.Name)
		}
	}

	var order []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		order = append(order, cur)
		for _, next := range adj[cur] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if len(order) != len(steps) {
		return nil, fmt.Errorf("pipeline has a cyclic dependency")
	}
	return order, nil
}
