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

// collectPodBlocks gathers pod blocks for a given key with criticalDs and controlPlane additions
func collectPodBlocks(blocks InjectorBlocks, key string, criticalDs, controlPlane bool) []string {
	injectedBlocks := getPodBlocksByKey(blocks["allPods"], key)
	if criticalDs {
		critDsBlocks := getPodBlocksByKey(blocks["criticalDsPods"], key)
		injectedBlocks = append(injectedBlocks, critDsBlocks...)
	}
	if controlPlane {
		cpBlocks := getPodBlocksByKey(blocks["controlPlanePods"], key)
		injectedBlocks = append(injectedBlocks, cpBlocks...)
	}
	return injectedBlocks
}

// mergeTolerations collects existing tolerations and merges with new blocks
// Returns updated result slice, next line index, and modification flags
func mergeTolerations(lines []string, result []string, i, actualIndent int, blocks []string, line string) ([]string, int, bool, bool) {
	result = append(result, line)
	var existingContent []string
	j := i + 1
	modified := false
	actuallyInjected := true

	// Collect existing tolerations content
	for j < len(lines) {
		nextLine := lines[j]
		nextIndent := getIndentation(nextLine)
		nextTrimmed := strings.TrimSpace(nextLine)

		if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
			result = append(result, nextLine)
			j++
			continue
		}

		if nextIndent <= actualIndent {
			break
		}

		result = append(result, nextLine)
		existingContent = append(existingContent, nextLine)
		j++
	}

	// Parse existing tolerations to structured format for comparison
	existingYaml := "tolerations:\n" + strings.Join(existingContent, "\n")
	var existingTolerations map[string]interface{}
	existingParsed := []map[string]interface{}{}
	if err := yaml.Unmarshal([]byte(existingYaml), &existingTolerations); err == nil {
		if tolList, ok := existingTolerations["tolerations"].([]interface{}); ok {
			for _, tol := range tolList {
				if tolMap, ok := tol.(map[interface{}]interface{}); ok {
					// Convert to string keys
					converted := make(map[string]interface{})
					for k, v := range tolMap {
						if keyStr, ok := k.(string); ok {
							converted[keyStr] = v
						}
					}
					existingParsed = append(existingParsed, converted)
				}
			}
		}
	}
	Logger.Infof("DEBUG mergeTolerations: Found %d existing tolerations", len(existingParsed))
	for idx, tol := range existingParsed {
		Logger.Infof("  Existing[%d]: %+v", idx, tol)
	}

	// Parse blocks to inject and collect only non-duplicate tolerations
	newTolerationsToAdd := []map[interface{}]interface{}{}

	Logger.Infof("DEBUG mergeTolerations: Checking %d blocks to inject", len(blocks))
	for blockIdx, block := range blocks {
		Logger.Infof("DEBUG Block[%d]:\n%s", blockIdx, block)
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			Logger.Infof("DEBUG Block[%d]: Failed to parse: %v", blockIdx, err)
			continue // Skip unparseable blocks
		}

		if tolList, ok := blockData["tolerations"].([]interface{}); ok {
			Logger.Infof("DEBUG Block[%d]: Found %d tolerations", blockIdx, len(tolList))
			for tolIdx, tol := range tolList {
				if tolMap, ok := tol.(map[interface{}]interface{}); ok {
					// Convert to string keys for comparison
					newTol := make(map[string]interface{})
					for k, v := range tolMap {
						if keyStr, ok := k.(string); ok {
							newTol[keyStr] = v
						}
					}

					Logger.Infof("DEBUG Block[%d] Tol[%d]: %+v", blockIdx, tolIdx, newTol)

					// Check if this toleration already exists
					exists := false
					for existIdx, existing := range existingParsed {
						if tolerationsMatch(existing, newTol) {
							Logger.Infof("DEBUG Block[%d] Tol[%d]: MATCHES Existing[%d]", blockIdx, tolIdx, existIdx)
							exists = true
							break
						}
					}

					// Only add if it doesn't exist
					if !exists {
						Logger.Infof("DEBUG Block[%d] Tol[%d]: NEW - adding to inject list", blockIdx, tolIdx)
						newTolerationsToAdd = append(newTolerationsToAdd, tolMap)
					}
				}
			}
		}
	}

	Logger.Infof("DEBUG mergeTolerations: Will inject %d new tolerations", len(newTolerationsToAdd))

	// If we have new tolerations to add, construct a single block and inject it
	if len(newTolerationsToAdd) > 0 {
		// Create a YAML block with just the new tolerations
		newBlock := map[string]interface{}{
			"tolerations": newTolerationsToAdd,
		}
		blockYaml, _ := yaml.Marshal(newBlock)
		blockStr := string(blockYaml)

		// Inject using standard method
		injected := injectBlockLines([]string{blockStr}, actualIndent+2, "tolerations")
		result = append(result, injected...)
		modified = true
	}

	return result, j, modified, actuallyInjected
}

// tolerationsMatch checks if two toleration maps are equivalent
func tolerationsMatch(a, b map[string]interface{}) bool {
	// Compare key fields: key, operator, effect, value, tolerationSeconds
	fields := []string{"key", "operator", "effect", "value", "tolerationSeconds"}
	for _, field := range fields {
		aVal, aHas := a[field]
		bVal, bHas := b[field]

		if aHas != bHas {
			return false
		}

		if aHas && fmt.Sprintf("%v", aVal) != fmt.Sprintf("%v", bVal) {
			return false
		}
	}
	return true
}

// handleComplexNestedBlock replaces existing content for complex blocks like affinity
// Returns updated result slice, next line index, and modification flags
func handleComplexNestedBlock(lines []string, result []string, i, actualIndent int, blocks []string, yl YAMLLine, replaceContent bool) ([]string, int, bool, bool) {
	j := i + 1
	hasExistingContent := false

	for j < len(lines) {
		nextLine := lines[j]
		nextIndent := getIndentation(nextLine)
		nextTrimmed := strings.TrimSpace(nextLine)

		if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
			if !replaceContent {
				result = append(result, nextLine)
			}
			j++
			continue
		}

		if nextIndent <= actualIndent {
			break
		}

		if !replaceContent {
			result = append(result, nextLine)
		}
		hasExistingContent = true
		j++
	}

	modified := false
	actuallyInjected := false

	if replaceContent {
		// Replace mode: inject new content
		result = append(result, strings.Repeat(" ", actualIndent)+yl.Key+":")
		injected := injectBlockLines(blocks, actualIndent+2, yl.Key)
		result = append(result, injected...)
		modified = true
		actuallyInjected = true
	} else if !hasExistingContent {
		// Check mode: only inject if no existing content
		injected := injectBlockLines(blocks, actualIndent+2, yl.Key)
		result = append(result, injected...)
		modified = true
		actuallyInjected = true
	}

	return result, j, modified, actuallyInjected
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

// getServiceBlocksByKey returns service blocks that have the specified top-level key
func getServiceBlocksByKey(blocks []string, key string) []string {
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
	//DEBUG
	//Logger.Infof("Injected lines for key=%s:\n%s", key, strings.Join(result, "\n"))
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

	// Convert to map[string]interface{} recursively to avoid JSON schema validation errors.
	// we assert the type after conversion
	valuesMap := convertMapI2MapS(valuesMapI).(map[string]interface{})

	// Now render the chart with updated values
	rel, err := renderChartLocal(chartPath, valuesMap)
	if err != nil {
		Logger.Errorf("error rendering chart: %s", err)
		return nil, err
	}
	return rel, nil
}
