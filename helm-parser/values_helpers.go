package helm_parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
	"helm.sh/helm/v3/pkg/release"
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

		// Handle single-line scalar values (e.g., "priorityClassName: system-node-critical")
		if len(blockLines) == 1 {
			// For single-line blocks, just add the line with proper indentation
			trimmedBlock := strings.TrimSpace(blockLines[0])
			result = append(result, strings.Repeat(" ", indent)+trimmedBlock)
			continue
		}

		// Skip the key line (e.g., "tolerations:" or "affinity:")
		if len(blockLines) < 2 {
			continue
		}

		// Get the base indentation from the first content line
		firstContentLine := blockLines[1]
		baseBlockIndent := GetIndentation(firstContentLine)

		for idx := 1; idx < len(blockLines); idx++ {
			blockLine := blockLines[idx]
			lineIndent := GetIndentation(blockLine)
			trimmedBlock := strings.TrimSpace(blockLine)

			// Calculate relative indentation from base
			relativeIndent := lineIndent - baseBlockIndent

			// Apply new indentation: base indent + relative indent
			result = append(result, strings.Repeat(" ", indent+relativeIndent)+trimmedBlock)
		}
	}

	return result
}

// isSingleLineScalar checks if all blocks for a key are single-line scalar values
func isSingleLineScalar(blocks []string, key string) bool {
	if len(blocks) == 0 {
		return false
	}

	for _, block := range blocks {
		blockLines := strings.Split(strings.TrimSpace(block), "\n")
		// Must be exactly 1 line and contain the key
		if len(blockLines) != 1 {
			return false
		}
		// Check if it's a simple key:value format (not nested)
		trimmed := strings.TrimSpace(blockLines[0])
		if !strings.HasPrefix(trimmed, key+":") {
			return false
		}
	}
	return true
}

func renderChartFromValues(chartPath string) (*release.Release, error) {
	// Read the updated values back for rendering
	valuesPath := filepath.Join(chartPath, "values.yaml")
	updatedValues, err := os.ReadFile(valuesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read updated values: %v", err)
	}

	// Parse updated values - unmarshal into map[interface{}]interface{} first
	var valuesMapI map[interface{}]interface{}
	if err := yaml.Unmarshal(updatedValues, &valuesMapI); err != nil {
		return nil, fmt.Errorf("failed to unmarshal updated values: %v", err)
	}

	// Convert to map[string]interface{} recursively to avoid JSON schema validation errors
	valuesMap := convertMapI2MapS(valuesMapI).(map[string]interface{})

	// Now render the chart with updated values
	rel, err := renderChartLocal(chartPath, valuesMap)
	if err != nil {
		Logger.Errorf("error rendering chart: %s", err)
		return nil, err
	}
	return rel, nil
}
