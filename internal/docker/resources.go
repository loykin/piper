package docker

import (
	"fmt"
	"strconv"

	"github.com/docker/go-units"
	"github.com/moby/moby/api/types/container"

	"github.com/piper/piper/pkg/manifest"
)

type ResourceSpec struct {
	Resources container.Resources
	ShmSize   int64
}

func ResourcesFromDriverDocker(ds *manifest.DriverDockerSpec) (ResourceSpec, error) {
	var out ResourceSpec
	if ds == nil {
		return out, nil
	}
	if ds.MemLimit != "" {
		n, err := units.RAMInBytes(ds.MemLimit)
		if err != nil {
			return out, fmt.Errorf("invalid docker memory %q: %w", ds.MemLimit, err)
		}
		out.Resources.Memory = n
	}
	if ds.ShmSize != "" {
		n, err := units.RAMInBytes(ds.ShmSize)
		if err != nil {
			return out, fmt.Errorf("invalid docker shm_size %q: %w", ds.ShmSize, err)
		}
		out.ShmSize = n
	}
	if ds.CPUs != "" {
		cpus, err := strconv.ParseFloat(ds.CPUs, 64)
		if err != nil || cpus < 0 {
			return out, fmt.Errorf("invalid docker cpus %q", ds.CPUs)
		}
		out.Resources.NanoCPUs = int64(cpus * 1_000_000_000)
	}
	if ds.Deploy != nil && ds.Deploy.Resources.Reservations != nil {
		for _, dev := range ds.Deploy.Resources.Reservations.Devices {
			if !hasCapability(dev.Capabilities, "gpu") {
				continue
			}
			req, err := DockerDeviceRequest(dev)
			if err != nil {
				return out, err
			}
			out.Resources.DeviceRequests = append(out.Resources.DeviceRequests, req)
		}
	}
	return out, nil
}

func DockerDeviceRequest(dev manifest.DockerDevice) (container.DeviceRequest, error) {
	driver := dev.Driver
	if driver == "" {
		driver = "nvidia"
	}
	if len(dev.DeviceIDs) > 0 {
		return container.DeviceRequest{
			Driver:       driver,
			DeviceIDs:    append([]string(nil), dev.DeviceIDs...),
			Capabilities: [][]string{{"gpu"}},
		}, nil
	}
	count := -1
	if dev.Count != "" && dev.Count != "all" {
		n, err := strconv.Atoi(dev.Count)
		if err != nil || n < 0 {
			return container.DeviceRequest{}, fmt.Errorf("invalid docker device count %q", dev.Count)
		}
		count = n
	}
	return container.DeviceRequest{
		Driver:       driver,
		Count:        count,
		Capabilities: [][]string{{"gpu"}},
	}, nil
}

func GPUDeviceRequestFromSelector(selector string) (container.DeviceRequest, error) {
	if selector == "" {
		return container.DeviceRequest{}, nil
	}
	if selector == "all" {
		return DockerDeviceRequest(manifest.DockerDevice{Count: "all", Capabilities: []string{"gpu"}})
	}
	return DockerDeviceRequest(manifest.DockerDevice{DeviceIDs: splitCSV(selector), Capabilities: []string{"gpu"}})
}

func hasCapability(capabilities []string, target string) bool {
	for _, capability := range capabilities {
		if capability == target {
			return true
		}
	}
	return false
}

func splitCSV(value string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(value); i++ {
		if i != len(value) && value[i] != ',' {
			continue
		}
		if part := value[start:i]; part != "" {
			out = append(out, part)
		}
		start = i + 1
	}
	return out
}
