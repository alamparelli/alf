package integrity

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"strings"
)

//go:embed ruleset.json
var rulesetJSON []byte

// SecurityRule is a single pattern rule loaded from ruleset.json.
type SecurityRule struct {
	ID        string   `json:"id"`
	Category  string   `json:"category"`
	Pattern   string   `json:"pattern"`
	Type      string   `json:"type"` // "substring" (default) or "regex" (future)
	Severity  string   `json:"severity"`
	CWE       string   `json:"cwe"`
	Reason    string   `json:"reason"`
	Languages []string `json:"languages"`
}

// SecurityRuleset is the top-level structure of ruleset.json.
type SecurityRuleset struct {
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Rules       []SecurityRule `json:"rules"`
}

// securityRuleset is the parsed ruleset, loaded once at init.
var securityRuleset SecurityRuleset

// Ruleset returns the parsed security ruleset. Read-only snapshot for
// consumers (e.g. Registry introspection).
func Ruleset() SecurityRuleset {
	return securityRuleset
}

func init() {
	if err := json.Unmarshal(rulesetJSON, &securityRuleset); err != nil {
		log.Fatalf("integrity: failed to parse embedded ruleset.json: %v", err)
	}
}

// SecurityWarning records a dangerous pattern found in a user tool.
type SecurityWarning struct {
	Tool     string `json:"tool"`
	RuleID   string `json:"rule_id"`
	Pattern  string `json:"pattern"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	CWE      string `json:"cwe"`
	Reason   string `json:"reason"`
}

// AuditToolSource scans a tool's source code for dangerous patterns from
// ruleset.json. Returns every matched warning (may be empty).
func AuditToolSource(toolPath, toolName string) []SecurityWarning {
	data, err := os.ReadFile(toolPath)
	if err != nil {
		return nil
	}
	src := strings.ToLower(string(data))
	var warnings []SecurityWarning
	for _, rule := range securityRuleset.Rules {
		if strings.Contains(src, strings.ToLower(rule.Pattern)) {
			warnings = append(warnings, SecurityWarning{
				Tool:     toolName,
				RuleID:   rule.ID,
				Pattern:  rule.Pattern,
				Category: rule.Category,
				Severity: rule.Severity,
				CWE:      rule.CWE,
				Reason:   rule.Reason,
			})
		}
	}
	return warnings
}
