package proto

import "context"

// Dispatcher는 ready 상태 task를 외부 실행 환경으로 dispatch한다.
// K8s Job, remote worker 등에서 구현.
// nil이면 worker 폴링 방식으로 동작한다.
type Dispatcher interface {
	Dispatch(ctx context.Context, task *Task) error
}
