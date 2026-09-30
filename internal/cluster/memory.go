package cluster

import (
	"context"
	"sync"
	"time"
)

// Memory is a set of instances sharing one process, for tests.
type Memory struct {
	mu     sync.Mutex
	leases map[string]Lease
}

func NewMemory() *Memory { return &Memory{leases: map[string]Lease{}} }

// Instance is one member of the set.
func (m *Memory) Instance(name string) Coordinator { return &memoryInstance{m, name} }

type memoryInstance struct {
	*Memory
	name string
}

func (i *memoryInstance) Instance() string { return i.name }

func (i *memoryInstance) Lease(_ context.Context, name string) (Lease, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.leases[name], nil
}

func (i *memoryInstance) Acquire(_ context.Context, name string) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	lease, now := i.leases[name], time.Now()
	if lease.Owner != i.name && lease.Live(now) {
		return false, nil
	}
	i.leases[name] = Lease{Owner: i.name, Until: now.Add(LeaseTTL)}
	return true, nil
}

func (i *memoryInstance) Release(_ context.Context, name string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.leases[name].Owner == i.name {
		delete(i.leases, name)
	}
	return nil
}
