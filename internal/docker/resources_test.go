package docker

import (
	"testing"

	"github.com/piper/piper/pkg/manifest"
)

func TestResourcesFromDriverDocker(t *testing.T) {
	spec, err := ResourcesFromDriverDocker(&manifest.DriverDockerSpec{
		CPUs:     "1.5",
		MemLimit: "512m",
		ShmSize:  "128m",
		Deploy: &manifest.DockerDeploySpec{Resources: manifest.DockerDeployResources{
			Reservations: &manifest.DockerReservations{Devices: []manifest.DockerDevice{{
				Driver:       "nvidia",
				Count:        "2",
				Capabilities: []string{"gpu"},
			}}},
		}},
	})
	if err != nil {
		t.Fatalf("ResourcesFromDriverDocker: %v", err)
	}
	if spec.Resources.NanoCPUs != 1_500_000_000 {
		t.Fatalf("NanoCPUs = %d", spec.Resources.NanoCPUs)
	}
	if spec.Resources.Memory != 512*1024*1024 {
		t.Fatalf("Memory = %d", spec.Resources.Memory)
	}
	if spec.ShmSize != 128*1024*1024 {
		t.Fatalf("ShmSize = %d", spec.ShmSize)
	}
	if len(spec.Resources.DeviceRequests) != 1 {
		t.Fatalf("DeviceRequests = %#v", spec.Resources.DeviceRequests)
	}
	if got := spec.Resources.DeviceRequests[0]; got.Driver != "nvidia" || got.Count != 2 {
		t.Fatalf("DeviceRequest = %#v", got)
	}
}

func TestGPUDeviceRequestFromSelector(t *testing.T) {
	all, err := GPUDeviceRequestFromSelector("all")
	if err != nil {
		t.Fatalf("all selector: %v", err)
	}
	if all.Count != -1 {
		t.Fatalf("all Count = %d", all.Count)
	}
	selected, err := GPUDeviceRequestFromSelector("0,1")
	if err != nil {
		t.Fatalf("selected selector: %v", err)
	}
	if got := selected.DeviceIDs; len(got) != 2 || got[0] != "0" || got[1] != "1" {
		t.Fatalf("DeviceIDs = %#v", got)
	}
}

func TestResourcesFromDriverDockerAllowsZeroValues(t *testing.T) {
	spec, err := ResourcesFromDriverDocker(&manifest.DriverDockerSpec{
		CPUs: "0",
		Deploy: &manifest.DockerDeploySpec{Resources: manifest.DockerDeployResources{
			Reservations: &manifest.DockerReservations{Devices: []manifest.DockerDevice{{
				Count:        "0",
				Capabilities: []string{"gpu"},
			}}},
		}},
	})
	if err != nil {
		t.Fatalf("ResourcesFromDriverDocker: %v", err)
	}
	if spec.Resources.NanoCPUs != 0 {
		t.Fatalf("NanoCPUs = %d, want 0", spec.Resources.NanoCPUs)
	}
	if got := spec.Resources.DeviceRequests[0].Count; got != 0 {
		t.Fatalf("device count = %d, want 0", got)
	}
}
