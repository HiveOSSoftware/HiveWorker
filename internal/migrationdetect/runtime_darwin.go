package migrationdetect

type RuntimeContainer struct {
	UUID      string            `json:"uuid"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	State     string            `json:"state"`
	Status    string            `json:"status"`
	MatchedBy []string          `json:"matched_by"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type RuntimeCheck struct {
	Available         bool               `json:"available"`
	Engine            string             `json:"engine,omitempty"`
	AllStopped        *bool              `json:"all_stopped,omitempty"`
	MatchedCount      int                `json:"matched_count"`
	ActiveCount       int                `json:"active_count"`
	ActiveContainers  []RuntimeContainer `json:"active_containers"`
	MatchedContainers []RuntimeContainer `json:"matched_containers"`
	Error             string             `json:"error,omitempty"`
}

func inspectSourceRuntime(uuids []string) RuntimeCheck {
	return RuntimeCheck{
		Available:         false,
		Engine:            "docker",
		MatchedContainers: []RuntimeContainer{},
		ActiveContainers:  []RuntimeContainer{},
		Error:             "automatic source-container detection is only available on Linux Workers",
	}
}
