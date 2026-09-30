package cluster

import (
	"context"
	"sync"
	"time"
)

// Memory is a set of instances sharing one process, for tests: each Instance
// behaves as a separate server would, and they coordinate only through here.
type Memory struct {
	mu      sync.Mutex
	now     func() time.Time
	leases  map[string]Lease
	servers map[string]map[string]func(context.Context, Call) Reply
	subs    map[string]map[*memorySub]bool
}

type memorySub struct {
	deliver func(string, []byte)
}

func NewMemory() *Memory {
	return &Memory{
		now:     time.Now,
		leases:  map[string]Lease{},
		servers: map[string]map[string]func(context.Context, Call) Reply{},
		subs:    map[string]map[*memorySub]bool{},
	}
}

// Instance is one member of the set.
func (m *Memory) Instance(name string) Coordinator { return &memoryInstance{m, name} }

// Expire ends a workspace's lease, as when its owner dies without releasing.
func (m *Memory) Expire(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease := m.leases[key]
	lease.Until = time.Time{}
	m.leases[key] = lease
}

type memoryInstance struct {
	*Memory
	name string
}

func (i *memoryInstance) Instance() string { return i.name }

func (i *memoryInstance) Lease(_ context.Context, key string) (Lease, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.leases[key], nil
}

func (i *memoryInstance) Acquire(_ context.Context, key string) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	lease, now := i.leases[key], i.now()
	if lease.Closing || (lease.Owner != i.name && lease.Live(now)) {
		return false, nil
	}
	i.leases[key] = Lease{Owner: i.name, Until: now.Add(LeaseTTL)}
	return true, nil
}

func (i *memoryInstance) Release(_ context.Context, key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if lease := i.leases[key]; lease.Owner == i.name {
		i.leases[key] = Lease{Closing: lease.Closing}
	}
	return nil
}

func (i *memoryInstance) MarkClosing(_ context.Context, key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	lease := i.leases[key]
	lease.Closing = true
	i.leases[key] = lease
	return nil
}

func (i *memoryInstance) ClearClosing(_ context.Context, key string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.leases, key)
	return nil
}

func (i *memoryInstance) Forward(ctx context.Context, key, owner string, call Call) (Reply, error) {
	i.mu.Lock()
	handle := i.servers[key][owner]
	lease := i.leases[key]
	i.mu.Unlock()
	if handle == nil || lease.Owner != owner || !lease.Live(i.now()) {
		return Reply{}, ErrOwnerGone
	}
	return handle(ctx, call), nil
}

func (i *memoryInstance) Serve(ctx context.Context, key string, handle func(context.Context, Call) Reply) {
	i.mu.Lock()
	if i.servers[key] == nil {
		i.servers[key] = map[string]func(context.Context, Call) Reply{}
	}
	i.servers[key][i.name] = handle
	i.mu.Unlock()
	go func() {
		<-ctx.Done()
		i.mu.Lock()
		delete(i.servers[key], i.name)
		i.mu.Unlock()
	}()
}

func (i *memoryInstance) Publish(_ context.Context, key, name string, data []byte) error {
	i.mu.Lock()
	var subs []*memorySub
	for sub := range i.subs[key] {
		subs = append(subs, sub)
	}
	i.mu.Unlock()
	for _, sub := range subs {
		sub.deliver(name, append([]byte(nil), data...))
	}
	return nil
}

func (i *memoryInstance) Subscribe(ctx context.Context, key string, deliver func(string, []byte)) {
	sub := &memorySub{deliver: deliver}
	i.mu.Lock()
	if i.subs[key] == nil {
		i.subs[key] = map[*memorySub]bool{}
	}
	i.subs[key][sub] = true
	i.mu.Unlock()
	go func() {
		<-ctx.Done()
		i.mu.Lock()
		delete(i.subs[key], sub)
		i.mu.Unlock()
	}()
}
