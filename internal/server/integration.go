package server

import (
	"regexp"
	"strings"
)

// The Hermes integration an external agent installs: the skill it follows, the
// Wake Gate script that checks for asks every minute, and the direct-request
// worker the Wake Gate runs for pending direct requests. list_asks reports
// their versions and get_integration hands them over, so an agent whose
// sandbox allows only the MCP endpoint still keeps all three current.
type integrationFile struct {
	Version string `json:"version"`
	Text    string `json:"text,omitempty"`
}

var integration struct {
	skill, gate, worker integrationFile
}

var (
	skillVersion  = regexp.MustCompile(`(?ms)\A---\n.*?^\s*version:\s*["']?([^"'\n]+)`)
	gateVersion   = regexp.MustCompile(`(?m)^GATE_VERSION\s*=\s*["']([^"']+)["']`)
	workerVersion = regexp.MustCompile(`(?m)^WORKER_VERSION\s*=\s*["']([^"']+)["']`)
)

// SetIntegration gives the server the distributed skill, Wake Gate script, and
// direct-request worker. Their versions are read from the files themselves:
// the skill's metadata.version and each script's *_VERSION constant.
func SetIntegration(skill, gate, worker string) {
	integration.skill = integrationFile{Text: skill}
	if m := skillVersion.FindStringSubmatch(skill); m != nil {
		integration.skill.Version = strings.TrimSpace(m[1])
	}
	integration.gate = integrationFile{Text: gate}
	if m := gateVersion.FindStringSubmatch(gate); m != nil {
		integration.gate.Version = strings.TrimSpace(m[1])
	}
	integration.worker = integrationFile{Text: worker}
	if m := workerVersion.FindStringSubmatch(worker); m != nil {
		integration.worker.Version = strings.TrimSpace(m[1])
	}
}

// integrationVersions is what list_asks carries, or nil when nothing is set.
func integrationVersions() map[string]string {
	if integration.skill.Version == "" && integration.gate.Version == "" && integration.worker.Version == "" {
		return nil
	}
	return map[string]string{"skill": integration.skill.Version, "gate": integration.gate.Version, "worker": integration.worker.Version}
}
