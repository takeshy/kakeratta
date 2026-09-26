package server

import (
	"regexp"
	"strings"
)

// The Hermes integration an external agent installs: the skill it follows and
// the Wake Gate script that checks for asks every minute. list_asks reports
// their versions and get_integration hands them over, so an agent whose
// sandbox allows only the MCP endpoint still keeps both current.
type integrationFile struct {
	Version string `json:"version"`
	Text    string `json:"text,omitempty"`
}

var integration struct {
	skill, gate integrationFile
}

var (
	skillVersion = regexp.MustCompile(`(?ms)\A---\n.*?^\s*version:\s*["']?([^"'\n]+)`)
	gateVersion  = regexp.MustCompile(`(?m)^GATE_VERSION\s*=\s*["']([^"']+)["']`)
)

// SetIntegration gives the server the distributed skill and Wake Gate script.
// Their versions are read from the files themselves: the skill's
// metadata.version and the script's GATE_VERSION.
func SetIntegration(skill, gate string) {
	integration.skill = integrationFile{Text: skill}
	if m := skillVersion.FindStringSubmatch(skill); m != nil {
		integration.skill.Version = strings.TrimSpace(m[1])
	}
	integration.gate = integrationFile{Text: gate}
	if m := gateVersion.FindStringSubmatch(gate); m != nil {
		integration.gate.Version = strings.TrimSpace(m[1])
	}
}

// integrationVersions is what list_asks carries, or nil when nothing is set.
func integrationVersions() map[string]string {
	if integration.skill.Version == "" && integration.gate.Version == "" {
		return nil
	}
	return map[string]string{"skill": integration.skill.Version, "gate": integration.gate.Version}
}
