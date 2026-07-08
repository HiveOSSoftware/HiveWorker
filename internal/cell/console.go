package cell

import "errors"

func (m *Manager) Console(id string) ([]string, error) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	cell, exists := m.cells[id]
	if !exists {
		return nil, errors.New("cell not found")
	}

	lines := make([]string, len(cell.console))
	copy(lines, cell.console)

	return lines, nil
}

func (m *Manager) ClearConsole(id string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	cell, exists := m.cells[id]
	if !exists {
		return errors.New("cell not found")
	}

	cell.console = nil

	return nil
}

func (m *Manager) Subscribe(id string) (chan string, error) {
	return m.SubscribeWithHistory(id, true)
}

func (m *Manager) SubscribeWithHistory(id string, includeHistory bool) (chan string, error) {
	ch := make(chan string, 100)

	m.mutex.Lock()
	defer m.mutex.Unlock()

	cell, exists := m.cells[id]
	if !exists {
		close(ch)
		return nil, errors.New("cell not found")
	}

	if cell.subscribers == nil {
		cell.subscribers = map[chan string]bool{}
	}

	cell.subscribers[ch] = true

	if includeHistory {
		for _, line := range cell.console {
			select {
			case ch <- line:
			default:
				return ch, nil
			}
		}
	}

	return ch, nil
}

func (m *Manager) Unsubscribe(id string, ch chan string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	cell, exists := m.cells[id]
	if !exists {
		return
	}

	if cell.subscribers != nil {
		delete(cell.subscribers, ch)
	}

	close(ch)
}

func (m *Manager) broadcast(cell *Cell, line string) {
	cell.console = append(cell.console, line)
	m.trimConsole(cell)

	if cell.subscribers == nil {
		return
	}

	for ch := range cell.subscribers {
		select {
		case ch <- line:
		default:
			// Do not block the whole manager if a client is slow/disconnected.
		}
	}
}

func (m *Manager) trimConsole(cell *Cell) {
	maxLines := 300

	if len(cell.console) > maxLines {
		cell.console = cell.console[len(cell.console)-maxLines:]
	}
}
