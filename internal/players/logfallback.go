package players

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

type Player struct {
	Name       string    `json:"name"`
	UUID       string    `json:"uuid,omitempty"`
	Online     bool      `json:"online"`
	Source     string    `json:"source"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
}

const hivepanelDir = ".hivepanel"
const playersStateFile = "players.json"

var (
	joinRegex = regexp.MustCompile(`\]: ([A-Za-z0-9_]{3,16}) joined the game`)
	quitRegex = regexp.MustCompile(`\]: ([A-Za-z0-9_]{3,16}) left the game`)
	uuidRegex = regexp.MustCompile(`\]: UUID of player ([A-Za-z0-9_]{3,16}) is ([0-9a-fA-F-]{36})`)
)

func ListFromLogs(cellDir string) ([]Player, error) {
	state, _ := loadState(cellDir)

	latestLog := filepath.Join(cellDir, "logs", "latest.log")

	file, err := os.Open(latestLog)
	if err != nil {
		return sortedPlayers(state), nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	now := time.Now().UTC()

	for scanner.Scan() {
		line := scanner.Text()

		if match := uuidRegex.FindStringSubmatch(line); len(match) == 3 {
			name := match[1]
			uuid := match[2]

			player := state[name]
			player.Name = name
			player.UUID = uuid
			player.Source = "logs"
			player.LastSeenAt = now

			state[name] = player
			continue
		}

		if match := joinRegex.FindStringSubmatch(line); len(match) == 2 {
			name := match[1]

			player := state[name]
			player.Name = name
			player.Online = true
			player.Source = "logs"
			player.LastSeenAt = now

			state[name] = player
			continue
		}

		if match := quitRegex.FindStringSubmatch(line); len(match) == 2 {
			name := match[1]

			player := state[name]
			player.Name = name
			player.Online = false
			player.Source = "logs"
			player.LastSeenAt = now

			state[name] = player
		}
	}

	if err := scanner.Err(); err != nil {
		return sortedPlayers(state), err
	}

	_ = saveState(cellDir, state)

	return sortedPlayers(state), nil
}

func loadState(cellDir string) (map[string]Player, error) {
	path := filepath.Join(cellDir, hivepanelDir, playersStateFile)

	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]Player{}, err
	}

	var state map[string]Player

	if err := json.Unmarshal(data, &state); err != nil {
		return map[string]Player{}, err
	}

	if state == nil {
		state = map[string]Player{}
	}

	return state, nil
}

func saveState(cellDir string, state map[string]Player) error {
	dir := filepath.Join(cellDir, hivepanelDir)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, playersStateFile), data, 0644)
}

func sortedPlayers(state map[string]Player) []Player {
	players := make([]Player, 0, len(state))

	for _, player := range state {
		players = append(players, player)
	}

	sort.Slice(players, func(i, j int) bool {
		if players[i].Online != players[j].Online {
			return players[i].Online
		}

		return players[i].Name < players[j].Name
	})

	return players
}
