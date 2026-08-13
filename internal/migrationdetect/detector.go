package migrationdetect

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Request struct {
	UUIDs          []string `json:"uuids"`
	ConfiguredPath string   `json:"configured_path"`
}

type Candidate struct {
	Path          string   `json:"path"`
	Source        string   `json:"source"`
	MatchedUUIDs  []string `json:"matched_uuids"`
	MissingUUIDs  []string `json:"missing_uuids"`
	Readable      bool     `json:"readable"`
	TotalBytes    int64    `json:"total_bytes"`
	Confidence    string   `json:"confidence"`
	MatchCount    int      `json:"match_count"`
	ExpectedCount int      `json:"expected_count"`
}

type Result struct {
	Detected     bool        `json:"detected"`
	DataPath     string      `json:"data_path,omitempty"`
	PathTemplate string      `json:"path_template,omitempty"`
	Source       string      `json:"source,omitempty"`
	Confidence   string      `json:"confidence,omitempty"`
	MatchedUUIDs []string    `json:"matched_uuids"`
	MissingUUIDs []string    `json:"missing_uuids"`
	TotalBytes   int64       `json:"total_bytes"`
	FreeBytes    int64       `json:"free_bytes"`
	EnoughSpace  *bool       `json:"enough_space,omitempty"`
	Candidates   []Candidate `json:"candidates"`
	Warnings     []string    `json:"warnings"`
}

func Detect(request Request) Result {
	uuids := normaliseUUIDs(request.UUIDs)
	candidates := collectCandidates(strings.TrimSpace(request.ConfiguredPath))

	results := make([]Candidate, 0, len(candidates))

	for path, source := range candidates {
		result := inspectCandidate(path, source, uuids)

		if result.MatchCount > 0 || result.Readable {
			results = append(results, result)
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].MatchCount != results[j].MatchCount {
			return results[i].MatchCount > results[j].MatchCount
		}

		return confidenceScore(results[i].Confidence) >
			confidenceScore(results[j].Confidence)
	})

	response := Result{
		MatchedUUIDs: []string{},
		MissingUUIDs: uuids,
		Candidates:   results,
		Warnings:     []string{},
	}

	if len(results) == 0 {
		response.Warnings = append(
			response.Warnings,
			"No readable local server data root could be detected.",
		)

		return response
	}

	best := results[0]

	if best.MatchCount == 0 {
		response.Warnings = append(
			response.Warnings,
			"Candidate data roots were found, but none contained the selected source server UUID directories.",
		)

		return response
	}

	response.Detected = true
	response.DataPath = best.Path
	response.PathTemplate = filepath.Join(best.Path, "{uuid}")
	response.Source = best.Source
	response.Confidence = best.Confidence
	response.MatchedUUIDs = best.MatchedUUIDs
	response.MissingUUIDs = best.MissingUUIDs
	response.TotalBytes = best.TotalBytes

	freeBytes, err := freeSpace(best.Path)
	if err == nil {
		response.FreeBytes = freeBytes

		enough := freeBytes > best.TotalBytes
		response.EnoughSpace = &enough

		if !enough {
			response.Warnings = append(
				response.Warnings,
				"The destination filesystem may not have enough free space for a rollback-safe local copy.",
			)
		}
	}

	if len(best.MissingUUIDs) > 0 {
		response.Warnings = append(
			response.Warnings,
			fmt.Sprintf(
				"%d of %d selected source server volume directories were not found under the detected data root.",
				len(best.MissingUUIDs),
				len(uuids),
			),
		)
	}

	return response
}

func collectCandidates(configuredPath string) map[string]string {
	candidates := map[string]string{}

	addCandidate := func(path string, source string) {
		path = cleanRoot(path)

		if path == "" {
			return
		}

		if _, exists := candidates[path]; !exists {
			candidates[path] = source
		}
	}

	if configuredPath != "" {
		addCandidate(configuredPath, "configured_path")
	}

	for _, configPath := range []string{
		"/etc/pterodactyl/config.yml",
		"/etc/pterodactyl/config.yaml",
		"/etc/wings/config.yml",
		"/etc/wings/config.yaml",
	} {
		if path := dataPathFromYAML(configPath); path != "" {
			addCandidate(path, "daemon_config:"+configPath)
		}
	}

	for _, configPath := range discoverSystemdConfigFiles() {
		if path := dataPathFromYAML(configPath); path != "" {
			addCandidate(path, "systemd_daemon_config:"+configPath)
		}
	}

	for _, path := range []string{
		"/var/lib/pterodactyl/volumes",
		"/var/lib/pterodactyl",
		"/mnt/data/pterodactyl/volumes",
		"/srv/pterodactyl/volumes",
		"/srv/pterodactyl",
		"/opt/pterodactyl/volumes",
	} {
		addCandidate(path, "conventional_path")
	}

	return candidates
}

func inspectCandidate(
	path string,
	source string,
	uuids []string,
) Candidate {
	result := Candidate{
		Path:          path,
		Source:        source,
		MatchedUUIDs:  []string{},
		MissingUUIDs:  []string{},
		ExpectedCount: len(uuids),
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		result.MissingUUIDs = append(
			result.MissingUUIDs,
			uuids...,
		)

		return result
	}

	result.Readable = directoryReadable(path)

	for _, uuid := range uuids {
		serverPath := filepath.Join(path, uuid)
		serverInfo, err := os.Stat(serverPath)

		if err != nil || !serverInfo.IsDir() {
			result.MissingUUIDs = append(
				result.MissingUUIDs,
				uuid,
			)
			continue
		}

		if !directoryReadable(serverPath) {
			result.MissingUUIDs = append(
				result.MissingUUIDs,
				uuid,
			)
			continue
		}

		result.MatchedUUIDs = append(
			result.MatchedUUIDs,
			uuid,
		)

		result.TotalBytes += directorySize(serverPath)
	}

	result.MatchCount = len(result.MatchedUUIDs)

	switch {
	case len(uuids) > 0 && result.MatchCount == len(uuids):
		result.Confidence = "high"
	case result.MatchCount > 0:
		result.Confidence = "medium"
	case strings.HasPrefix(source, "daemon_config:") ||
		strings.HasPrefix(source, "systemd_daemon_config:"):
		result.Confidence = "low"
	default:
		result.Confidence = "low"
	}

	return result
}

func discoverSystemdConfigFiles() []string {
	paths := []string{}
	seen := map[string]bool{}

	serviceDirs := []string{
		"/etc/systemd/system",
		"/usr/lib/systemd/system",
		"/lib/systemd/system",
	}

	for _, dir := range serviceDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".service") {
				continue
			}

			servicePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(servicePath)
			if err != nil {
				continue
			}

			content := string(data)
			lower := strings.ToLower(content)

			if !strings.Contains(lower, "wings") &&
				!strings.Contains(lower, "elytra") &&
				!strings.Contains(lower, "pterodactyl") {
				continue
			}

			for _, configPath := range configPathsFromService(content) {
				if configPath == "" || seen[configPath] {
					continue
				}

				seen[configPath] = true
				paths = append(paths, configPath)
			}
		}
	}

	return paths
}

func configPathsFromService(content string) []string {
	result := []string{}

	scanner := bufio.NewScanner(strings.NewReader(content))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}

		fields := strings.Fields(
			strings.TrimPrefix(line, "ExecStart="),
		)

		for index, field := range fields {
			field = strings.Trim(field, `"'`)

			if field == "--config" || field == "-c" {
				if index+1 < len(fields) {
					result = append(
						result,
						strings.Trim(fields[index+1], `"'`),
					)
				}

				continue
			}

			if strings.HasPrefix(field, "--config=") {
				result = append(
					result,
					strings.Trim(
						strings.TrimPrefix(field, "--config="),
						`"'`,
					),
				)
			}
		}
	}

	return result
}

func dataPathFromYAML(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	inSystem := false
	systemIndent := -1

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, " \t\r")

		if strings.TrimSpace(line) == "" ||
			strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}

		indent := leadingWhitespace(line)
		trimmed := strings.TrimSpace(line)

		if trimmed == "system:" {
			inSystem = true
			systemIndent = indent
			continue
		}

		if inSystem && indent <= systemIndent {
			inSystem = false
		}

		if inSystem {
			for _, key := range []string{
				"data:",
				"data_directory:",
				"server_data:",
				"server_data_directory:",
				"volumes:",
				"volumes_directory:",
			} {
				if strings.HasPrefix(trimmed, key) {
					return yamlScalarValue(
						strings.TrimPrefix(trimmed, key),
					)
				}
			}
		}
	}

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)

		for _, key := range []string{
			"data_directory:",
			"server_data:",
			"server_data_directory:",
			"volumes_directory:",
		} {
			if strings.HasPrefix(trimmed, key) {
				return yamlScalarValue(
					strings.TrimPrefix(trimmed, key),
				)
			}
		}
	}

	return ""
}

func yamlScalarValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)

	if hash := strings.Index(value, " #"); hash >= 0 {
		value = strings.TrimSpace(value[:hash])
	}

	return cleanRoot(value)
}

func cleanRoot(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"'`)

	if path == "" || !filepath.IsAbs(path) {
		return ""
	}

	return filepath.Clean(path)
}

func normaliseUUIDs(uuids []string) []string {
	result := []string{}
	seen := map[string]bool{}

	for _, uuid := range uuids {
		uuid = strings.ToLower(strings.TrimSpace(uuid))

		if uuid == "" || seen[uuid] {
			continue
		}

		seen[uuid] = true
		result = append(result, uuid)
	}

	sort.Strings(result)

	return result
}

func directoryReadable(path string) bool {
	dir, err := os.Open(path)
	if err != nil {
		return false
	}
	defer dir.Close()

	_, err = dir.Readdirnames(1)

	return err == nil || err.Error() == "EOF"
}

func directorySize(root string) int64 {
	var total int64

	_ = filepath.WalkDir(
		root,
		func(
			path string,
			entry os.DirEntry,
			err error,
		) error {
			if err != nil {
				return nil
			}

			if entry.Type().IsRegular() {
				info, infoErr := entry.Info()
				if infoErr == nil {
					total += info.Size()
				}
			}

			return nil
		},
	)

	return total
}

func leadingWhitespace(value string) int {
	return len(value) - len(strings.TrimLeft(value, " \t"))
}

func confidenceScore(value string) int {
	switch value {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
