package notebook

import (
	"slices"
	"strings"

	"github.com/piper/piper/pkg/manifest"
)

// Notebook is the top-level structure of a piper Notebook YAML definition.
type Notebook struct {
	manifest.TypeMeta `yaml:",inline"`
	Metadata          manifest.ObjectMeta `yaml:"metadata"`
	Spec              NotebookSpec        `yaml:"spec"`
}

// Name returns the notebook name from metadata.
func (n Notebook) Name() string { return n.Metadata.Name }

// WorkerID returns the placement worker ID, or empty string if unset.
func (n Notebook) WorkerID() string { return n.Spec.Driver.Placement.Worker }

// StorageSize returns the PVC storage size from the volume spec, or empty if unset.
func (n Notebook) StorageSize() string {
	if n.Spec.Volume != nil {
		return n.Spec.Volume.Size
	}
	return ""
}

// GPURequest returns a GPU device-ID selector string used to route the notebook
// to a worker that holds those specific GPUs.
//
// For process: driver.process.gpus field (e.g. "0,1" or "all").
// For docker: device_ids of the first gpu-capable device, or empty if only count is set.
// For k8s: always empty (node selection via driver.k8s.pod_template.spec.nodeSelector).
func (n Notebook) GPURequest() string {
	if p := n.Spec.Driver.Process; p != nil && p.GPUs != "" {
		return p.GPUs
	}
	if d := n.Spec.Driver.Docker; d != nil && d.Deploy != nil && d.Deploy.Resources.Reservations != nil {
		for _, dev := range d.Deploy.Resources.Reservations.Devices {
			if !slices.Contains(dev.Capabilities, "gpu") {
				continue
			}
			if len(dev.DeviceIDs) > 0 {
				return strings.Join(dev.DeviceIDs, ",")
			}
			return ""
		}
	}
	return ""
}

type NotebookSpec struct {
	Options manifest.SpecOptions `yaml:"options,omitempty"`
	Prepare *NotebookPrepareSpec `yaml:"prepare,omitempty"`
	Volume  *VolumeSpec          `yaml:"volume,omitempty"`
	Driver  manifest.DriverSpec  `yaml:"driver"`
}

// VolumeSpec describes notebook-specific persistent storage.
type VolumeSpec struct {
	Size         string `yaml:"size,omitempty"`
	StorageClass string `yaml:"storage_class,omitempty"` // K8s storage class
}

// NotebookPrepareSpec describes pre-start work that can run before notebook launch.
type NotebookPrepareSpec struct {
	Presets []string              `yaml:"presets,omitempty"`
	Steps   []NotebookPrepareStep `yaml:"steps,omitempty"`
}

// NotebookPrepareStep represents one explicit pre-start action.
// Backend may be empty (applies to all backends) or one of: process, docker, k8s.
type NotebookPrepareStep struct {
	Type    string   `yaml:"type,omitempty"`
	Backend string   `yaml:"backend,omitempty"`
	Command []string `yaml:"command,omitempty"`
}
