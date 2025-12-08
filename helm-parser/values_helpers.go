package helm_parser

import (
	"strings"

	"gopkg.in/yaml.v2"
)

// pathsMatch checks if the current path matches the target path
func pathsMatch(current, target []string) bool {
	if len(current) != len(target) {
		return false
	}
	for i := range current {
		if current[i] != target[i] {
			return false
		}
	}
	return true
}

// containsLine checks if a trimmed line exists in a list of trimmed lines
func containsLine(lines []string, target string) bool {
	for _, line := range lines {
		if line == target {
			return true
		}
	}
	return false
}

// getContainerBlocksByKey returns container blocks that have the specified top-level key
func getContainerBlocksByKey(blocks []string, key string) []string {
	var result []string
	for _, block := range blocks {
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			continue
		}
		if _, ok := blockData[key]; ok {
			result = append(result, block)
		}
	}
	return result
}

// extractContainerBlockKeys extracts all unique top-level keys from container blocks
func extractContainerBlockKeys(blocks []string) []string {
	keysMap := make(map[string]bool)
	for _, block := range blocks {
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			continue
		}
		for key := range blockData {
			keysMap[key] = true
		}
	}

	var keys []string
	for key := range keysMap {
		keys = append(keys, key)
	}
	return keys
}

// isComplexNestedBlock checks if a key represents a complex nested structure (like resources)
// by looking at the blocks to see if the value is a nested map (not a list)
func isComplexNestedBlock(key string, injectedBlocks []string) bool {
	// Affinity is always complex
	if key == "affinity" {
		return true
	}

	for _, block := range injectedBlocks {
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			continue
		}
		if value, ok := blockData[key]; ok {
			// If value is a map, it's a complex nested structure
			if _, isMap := value.(map[interface{}]interface{}); isMap {
				return true
			}
			if _, isMap := value.(map[string]interface{}); isMap {
				return true
			}
		}
	}
	return false
}

// isListBasedBlock checks if a key represents a list-based block (like envFrom, env)
func isListBasedBlock(key string, injectedBlocks []string) bool {
	// Tolerations is always list-based
	if key == "tolerations" {
		return true
	}

	for _, block := range injectedBlocks {
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			continue
		}
		if value, ok := blockData[key]; ok {
			// If value is a list/array, it's list-based
			if _, isList := value.([]interface{}); isList {
				return true
			}
		}
	}
	return false
}

// injectBlockLines converts block YAML strings to lines with proper indentation
func injectBlockLines(blocks []string, indent int, key string) []string {
	var result []string

	for _, block := range blocks {
		blockLines := strings.Split(strings.TrimSpace(block), "\n")
		// Skip the key line (e.g., "tolerations:" or "affinity:")
		if len(blockLines) < 2 {
			continue
		}

		// Get the base indentation from the first content line
		firstContentLine := blockLines[1]
		baseBlockIndent := getIndentation(firstContentLine)

		for idx := 1; idx < len(blockLines); idx++ {
			blockLine := blockLines[idx]
			lineIndent := getIndentation(blockLine)
			trimmedBlock := strings.TrimSpace(blockLine)

			// Calculate relative indentation from base
			relativeIndent := lineIndent - baseBlockIndent

			// Apply new indentation: base indent + relative indent
			result = append(result, strings.Repeat(" ", indent+relativeIndent)+trimmedBlock)
		}
	}

	return result
}
