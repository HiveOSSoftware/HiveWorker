package cell

import (
	"hivepanel-worker/internal/comb"
)

func (m *Manager) resolveCellComb(cell *Cell) (*comb.Comb, error) {
	if cell.CombData != nil {
		return comb.FromMap(cell.CombData)
	}

	return m.combManager.Require(cell.Comb)
}
