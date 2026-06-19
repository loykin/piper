package baremetaldriver

import (
	"testing"

	pipelinedriver "github.com/piper/piper/pkg/pipeline/worker/driver"
	"github.com/piper/piper/pkg/pipeline/worker/driver/drivertest"
)

var _ pipelinedriver.Driver = (*Driver)(nil)

func TestBaremetalDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() pipelinedriver.Driver {
		d, err := New(Config{WorkerID: "contract-test", MetaDir: t.TempDir()})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	})
}
