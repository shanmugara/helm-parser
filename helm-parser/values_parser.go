package helm_parser

import (
	"strings"
)

// ValueReference represents a reference to a value in the values file
// e.g., .Values.tolerations or .Values.webhook.tolerations
type ValueReference struct {
	Path []string // e.g., ["tolerations"] or ["webhook", "tolerations"]
	Key  string   // e.g., "tolerations"
}

// DetectValueReferences scans template files to detect which .Values keys are referenced
// Returns a list of ValueReference with full paths (e.g., .Values.webhook.tolerations)
func DetectValueReferences(templateContent string) []ValueReference {
	var references []ValueReference
	seen := make(map[string]bool) // Track duplicates

	lines := strings.Split(templateContent, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Look for any .Values.XXX patterns
		if strings.Contains(trimmed, ".Values.") {
			// Find all .Values references in this line
			parts := strings.Split(trimmed, ".Values.")
			for i := 1; i < len(parts); i++ {
				keyPath := extractValuePath(parts[i])
				if keyPath != "" && !seen[keyPath] {
					seen[keyPath] = true
					ref := parseValuePath(keyPath)
					if ref.Key != "" {
						references = append(references, ref)
					}
				}
			}
		}
	}

	return references
}

// extractValuePath extracts the path after .Values. up to the next delimiter
// e.g., "webhook.tolerations }}" -> "webhook.tolerations"
func extractValuePath(s string) string {
	path := ""
	for _, ch := range s {
		if ch == ' ' || ch == '}' || ch == ')' || ch == '|' || ch == ',' {
			break
		}
		path += string(ch)
	}
	return strings.TrimSpace(path)
}

// parseValuePath converts a dot-separated path to a ValueReference
// e.g., "webhook.tolerations" -> ValueReference{Path: ["webhook", "tolerations"], Key: "tolerations"}
// e.g., "tolerations" -> ValueReference{Path: ["tolerations"], Key: "tolerations"}
func parseValuePath(pathStr string) ValueReference {
	parts := strings.Split(pathStr, ".")
	if len(parts) == 0 {
		return ValueReference{}
	}

	// The last part is the key we care about (tolerations, affinity, resources, envFrom, env, etc.)
	key := parts[len(parts)-1]

	// Accept all keys - we'll filter by available blocks in InjectIntoValuesFile
	return ValueReference{
		Path: parts,
		Key:  key,
	}
}

// detectWrapperPattern detects if values.yaml uses a wrapper pattern
// like Istio's _internal_defaults_do_not_set and returns the indent offset
func detectWrapperPattern(content string) int {
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check if first non-comment key is a wrapper pattern
		if strings.Contains(trimmed, ":") {
			indent := getIndentation(line)
			if indent == 0 {
				key := strings.TrimSpace(strings.Split(trimmed, ":")[0])
				// Check against known wrapper patterns
				for _, wrapperKey := range KnownWrapperKeys {
					if key == wrapperKey {
						// Return the expected indent of children (typically 2)
						return 2
					}
				}
			}
			break
		}
	}

	return 0
}
