package allocation

import (
	"errors"
	"fmt"
	"net"
	"sync"
)

type Allocation struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type Manager struct {
	mutex     sync.Mutex
	ip        string
	portStart int
	portEnd   int
	used      map[int]bool
}

func NewManager(ip string, portStart int, portEnd int) *Manager {
	return &Manager{
		ip:        ip,
		portStart: portStart,
		portEnd:   portEnd,
		used:      map[int]bool{},
	}
}

func (m *Manager) ReserveExisting(allocation Allocation) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if allocation.Port > 0 {
		m.used[allocation.Port] = true
	}
}

func (m *Manager) Allocate() (Allocation, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for port := m.portStart; port <= m.portEnd; port++ {
		if m.used[port] {
			continue
		}

		if !isPortFree(m.ip, port) {
			continue
		}

		m.used[port] = true

		return Allocation{
			IP:   m.ip,
			Port: port,
		}, nil
	}

	return Allocation{}, errors.New("no free allocations available")
}

func isPortFree(ip string, port int) bool {
	address := fmt.Sprintf("%s:%d", ip, port)

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}

	_ = listener.Close()
	return true
}

func (m *Manager) Release(allocation Allocation) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if allocation.Port > 0 {
		delete(m.used, allocation.Port)
	}
}
