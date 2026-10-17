// Package dockertest provides fakes for docker driver tests.
package dockertest

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"sync"

	"github.com/moby/moby/api/types/jsonstream"
	dockerclient "github.com/moby/moby/client"
)

// Images fakes the daemon's image store. Embed it in a fake client: every
// image is present unless listed in Missing, and ImagePull records the pull.
type Images struct {
	mu      sync.Mutex
	Missing map[string]bool
	Pulled  []string
}

func (f *Images) ImageInspect(_ context.Context, image string, _ ...dockerclient.ImageInspectOption) (dockerclient.ImageInspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Missing[image] {
		return dockerclient.ImageInspectResult{}, errors.New("No such image: " + image)
	}
	return dockerclient.ImageInspectResult{}, nil
}

func (f *Images) ImagePull(_ context.Context, image string, _ dockerclient.ImagePullOptions) (dockerclient.ImagePullResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Pulled = append(f.Pulled, image)
	delete(f.Missing, image)
	return pullResponse{io.NopCloser(strings.NewReader(""))}, nil
}

// CheckCreate fails like the daemon's ContainerCreate does for an image it
// does not have. Call it from a fake ContainerCreate.
func (f *Images) CheckCreate(image string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Missing[image] {
		return errors.New("Error response from daemon: No such image: " + image)
	}
	return nil
}

// PulledImages returns the images pulled so far.
func (f *Images) PulledImages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Pulled...)
}

type pullResponse struct{ io.ReadCloser }

func (pullResponse) JSONMessages(context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(func(jsonstream.Message, error) bool) {}
}

func (pullResponse) Wait(context.Context) error { return nil }
