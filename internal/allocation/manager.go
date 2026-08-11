package allocation

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
)

type Allocation struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type Manager struct {
	mutex     sync.Mutex
	ips       []string
	portStart int
	portEnd   int
	used      map[string]bool
}

func NewManager(ip string, portStart int, portEnd int, additionalIPs ...string) *Manager {
	ips := normaliseIPs(append([]string{ip}, additionalIPs...))

	return &Manager{
		ips:       ips,
		portStart: portStart,
		portEnd:   portEnd,
		used:      map[string]bool{},
	}
}

func (m *Manager) Configuration() ([]string, int, int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	return append([]string(nil), m.ips...), m.portStart, m.portEnd
}

func (m *Manager) Reconfigure(ips []string, portStart int, portEnd int) ([]string, error) {
	ips = normaliseIPs(ips)

	if err := validateConfiguration(ips, portStart, portEnd); err != nil {
		return nil, err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	for key := range m.used {
		host, portText, err := net.SplitHostPort(key)
		if err != nil {
			return nil, fmt.Errorf("invalid reserved allocation %q", key)
		}

		port, err := strconv.Atoi(portText)
		if err != nil {
			return nil, fmt.Errorf("invalid reserved allocation port %q", portText)
		}

		if !containsIP(ips, host) {
			return nil, fmt.Errorf(
				"cannot remove allocation IP %s while an existing Cell still uses %s",
				host,
				key,
			)
		}

		if port < portStart || port > portEnd {
			return nil, fmt.Errorf(
				"cannot change allocation port range while an existing Cell still uses %s",
				key,
			)
		}
	}

	m.ips = append([]string(nil), ips...)
	m.portStart = portStart
	m.portEnd = portEnd

	return append([]string(nil), m.ips...), nil
}

func (m *Manager) IPs() []string {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	return append([]string(nil), m.ips...)
}

func (m *Manager) AddIP(ip string) {
	ip = strings.TrimSpace(ip)

	if ip == "" {
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	for _, existing := range m.ips {
		if existing == ip {
			return
		}
	}

	m.ips = append(m.ips, ip)
}

func (m *Manager) RemoveIP(ip string) {
	ip = strings.TrimSpace(ip)

	if ip == "" {
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	filtered := make([]string, 0, len(m.ips))

	for _, existing := range m.ips {
		if existing != ip {
			filtered = append(filtered, existing)
		}
	}

	m.ips = filtered
}

func (m *Manager) ReserveExisting(allocation Allocation) {
	allocation.IP = strings.TrimSpace(allocation.IP)

	if allocation.IP == "" || allocation.Port <= 0 {
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.used[allocationKey(allocation)] = true
}

func (m *Manager) ReserveMany(allocations []Allocation) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for _, allocation := range allocations {
		allocation.IP = strings.TrimSpace(allocation.IP)

		if allocation.IP == "" || allocation.Port <= 0 {
			continue
		}

		m.used[allocationKey(allocation)] = true
	}
}

func (m *Manager) ReplaceReservations(oldAllocations []Allocation, newAllocations []Allocation) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for _, allocation := range oldAllocations {
		allocation.IP = strings.TrimSpace(allocation.IP)

		if allocation.IP == "" || allocation.Port <= 0 {
			continue
		}

		delete(m.used, allocationKey(allocation))
	}

	for _, allocation := range newAllocations {
		allocation.IP = strings.TrimSpace(allocation.IP)

		if allocation.IP == "" || allocation.Port <= 0 {
			continue
		}

		m.used[allocationKey(allocation)] = true
	}
}

func (m *Manager) Allocate() (Allocation, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if len(m.ips) == 0 {
		return Allocation{}, errors.New("no allocation IPs configured")
	}

	for _, ip := range m.ips {
		for port := m.portStart; port <= m.portEnd; port++ {
			allocation := Allocation{
				IP:   ip,
				Port: port,
			}

			key := allocationKey(allocation)

			if m.used[key] {
				continue
			}

			if !isPortFree(ip, port) {
				continue
			}

			m.used[key] = true

			return allocation, nil
		}
	}

	return Allocation{}, errors.New("no free allocations available")
}

func (m *Manager) AllocateForIP(ip string) (Allocation, error) {
	ip = strings.TrimSpace(ip)

	if ip == "" {
		return Allocation{}, errors.New("allocation IP is required")
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !containsIP(m.ips, ip) {
		return Allocation{}, errors.New("allocation IP is not configured")
	}

	for port := m.portStart; port <= m.portEnd; port++ {
		allocation := Allocation{
			IP:   ip,
			Port: port,
		}

		key := allocationKey(allocation)

		if m.used[key] {
			continue
		}

		if !isPortFree(ip, port) {
			continue
		}

		m.used[key] = true

		return allocation, nil
	}

	return Allocation{}, errors.New("no free allocations available for IP")
}

func (m *Manager) IsReserved(allocation Allocation) bool {
	allocation.IP = strings.TrimSpace(allocation.IP)

	if allocation.IP == "" || allocation.Port <= 0 {
		return false
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	return m.used[allocationKey(allocation)]
}

func (m *Manager) Release(allocation Allocation) {
	allocation.IP = strings.TrimSpace(allocation.IP)

	if allocation.IP == "" || allocation.Port <= 0 {
		return
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	delete(m.used, allocationKey(allocation))
}

func allocationKey(allocation Allocation) string {
	return net.JoinHostPort(
		strings.TrimSpace(allocation.IP),
		strconv.Itoa(allocation.Port),
	)
}

func isPortFree(ip string, port int) bool {
	address := net.JoinHostPort(
		strings.TrimSpace(ip),
		strconv.Itoa(port),
	)

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}

	_ = listener.Close()

	return true
}

func validateConfiguration(ips []string, portStart int, portEnd int) error {
	if len(ips) == 0 {
		return errors.New("at least one allocation IP is required")
	}

	for _, ip := range ips {
		if ip == "0.0.0.0" || ip == "::" {
			continue
		}

		if net.ParseIP(ip) == nil {
			return fmt.Errorf("invalid allocation IP address: %s", ip)
		}
	}

	if portStart < 1 || portStart > 65535 {
		return errors.New("allocation port_start must be between 1 and 65535")
	}

	if portEnd < 1 || portEnd > 65535 {
		return errors.New("allocation port_end must be between 1 and 65535")
	}

	if portEnd < portStart {
		return errors.New("allocation port_end must be greater than or equal to port_start")
	}

	return nil
}

func normaliseIPs(ips []string) []string {
	result := make([]string, 0, len(ips))
	seen := map[string]bool{}

	for _, ip := range ips {
		ip = strings.TrimSpace(ip)

		if ip == "" || seen[ip] {
			continue
		}

		seen[ip] = true
		result = append(result, ip)
	}

	return result
}

func containsIP(ips []string, ip string) bool {
	for _, existing := range ips {
		if existing == ip {
			return true
		}
	}

	return false
}
