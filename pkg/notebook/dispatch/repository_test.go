package notebookdispatch

import (
	"context"
	"errors"
	"sync"

	"github.com/piper/piper/pkg/notebook"
)

type fakeRepo struct {
	mu      sync.Mutex
	servers map[string]*notebook.NotebookServer
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{servers: make(map[string]*notebook.NotebookServer)}
}

func (r *fakeRepo) Create(_ context.Context, nb *notebook.NotebookServer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *nb
	r.servers[nb.Name] = &cp
	return nil
}

func (r *fakeRepo) Get(_ context.Context, name string) (*notebook.NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	nb, ok := r.servers[name]
	if !ok {
		return nil, nil
	}
	cp := *nb
	return &cp, nil
}

func (r *fakeRepo) Update(_ context.Context, nb *notebook.NotebookServer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.servers[nb.Name]; !ok {
		return errors.New("not found")
	}
	cp := *nb
	r.servers[nb.Name] = &cp
	return nil
}

func (r *fakeRepo) SetStatus(_ context.Context, name, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	nb, ok := r.servers[name]
	if !ok {
		return errors.New("not found")
	}
	nb.Status = status
	return nil
}

func (r *fakeRepo) GetByVolumeID(_ context.Context, volumeID string) (*notebook.NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, nb := range r.servers {
		if nb.VolumeID == volumeID {
			cp := *nb
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) List(_ context.Context) ([]*notebook.NotebookServer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*notebook.NotebookServer, 0, len(r.servers))
	for _, nb := range r.servers {
		cp := *nb
		out = append(out, &cp)
	}
	return out, nil
}

func (r *fakeRepo) Delete(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.servers, name)
	return nil
}
