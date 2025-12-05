package helm_parser

import (
"strings"

"gopkg.in/yaml.v2"
)

// injectInlineContainerSpec injects container-level blocks into Kubernetes resource templates
func injectInlineContainerSpec(content string) (string, error) {
	blocks, err := loadInjectorBlocks()
	if err != nil {
		return "", err
	}
	return injectInlineContainerSpecWithBlocks(content, blocks)
}

func injectInlineContainerSpecWithBlocks(content string, blocks InjectorBlocks) (string, error) {
	lines := strings.Split(content, "\n")
	var result []string
	i := 0

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check if this is a container name definition
		isContainer := strings.HasPrefix(trimmed, "- name:") && isUnderContainersSection(lines, i)

		if isContainer {
			// Add the container name line
			result = append(result, line)

			// Find the indentation level of the container
			indent := getIndentation(line)

			// Check which blocks from allContainers are missing
			containerBlocks := blocks["allContainers"]
			missingBlocks := findMissingBlocks(lines, i, indent, containerBlocks)

			if len(missingBlocks) > 0 {
				// Check if we have envFrom blocks to inject and if envFrom already exists
				envFromBlocks := getBlocksByKey(missingBlocks, "envFrom")
				existingEnvFromIdx := findExistingKey(lines, i, indent, "envFrom")

				if len(envFromBlocks) > 0 && existingEnvFromIdx != -1 {
					// envFrom exists, append our items to it
					insertionPoint := findEnvFromEndPoint(lines, existingEnvFromIdx, indent)

					// Copy lines up to the insertion point
					for j := i + 1; j < insertionPoint; j++ {
						result = append(result, lines[j])
					}

					// Append our envFrom items
					spaces := strings.Repeat(" ", indent+2)
					for _, block := range envFromBlocks {
						blockLines := strings.Split(strings.TrimSpace(block), "\n")
						// Skip the "envFrom:" line and add the rest
						for idx := 1; idx < len(blockLines); idx++ {
							result = append(result, spaces+blockLines[idx])
						}
					}

					// Remove envFrom blocks from missing list
					missingBlocks = removeBlocksByKey(missingBlocks, "envFrom")

					// Now handle remaining blocks
					if len(missingBlocks) > 0 {
						injectionPoint := findBlockInjectionPoint(lines, insertionPoint-1, indent)
						// Copy from insertion point to injection point
						for j := insertionPoint; j < injectionPoint; j++ {
							result = append(result, lines[j])
						}
						// Inject remaining blocks
						result = append(result, injectMissingBlocks(missingBlocks, indent+2)...)
						i = injectionPoint
					} else {
						i = insertionPoint
					}
				} else {
					// No existing envFrom or no envFrom blocks to inject
					injectionPoint := findBlockInjectionPoint(lines, i, indent)

					// Add lines from after container name up to injection point
					for j := i + 1; j < injectionPoint; j++ {
						result = append(result, lines[j])
					}

					// Inject the missing blocks
					result = append(result, injectMissingBlocks(missingBlocks, indent+2)...)

					// Continue from injection point
					i = injectionPoint
				}
			} else {
				// All blocks already exist, just copy lines until next container
				j := i + 1
				for j < len(lines) {
					nextLine := lines[j]
					nextIndent := getIndentation(nextLine)
					nextTrimmed := strings.TrimSpace(nextLine)

					// Stop when we hit another container or exit this container
					if nextIndent <= indent && nextTrimmed != "" && !strings.HasPrefix(nextTrimmed, "#") {
						break
					}
					result = append(result, nextLine)
					j++
				}
				i = j
			}
		} else {
			// Not a container definition, just add the line
			result = append(result, line)
			i++
		}
	}

	return strings.Join(result, "\n"), nil
}

// isUnderContainersSection checks if a line index is under a containers: section
func isUnderContainersSection(lines []string, index int) bool {
	// Look backwards to find the immediate parent section
	// We need to find what section this "- name:" belongs to
	lineIndent := getIndentation(lines[index])

	// First, check sibling lines at the same indent level
	for i := index - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		indent := getIndentation(lines[i])

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check sibling lines (same indentation)
		if indent == lineIndent {
			if strings.HasPrefix(trimmed, "env:") ||
				strings.HasPrefix(trimmed, "envFrom:") ||
				strings.HasPrefix(trimmed, "args:") ||
				strings.HasPrefix(trimmed, "volumeMounts:") ||
				strings.HasPrefix(trimmed, "ports:") {
				return false
			}
		}

		// If we find a line at lower indentation, it's a parent section
		if indent < lineIndent {
			// Check if the parent is containers:
			if strings.HasPrefix(trimmed, "containers:") {
				return true
			}
			// If parent is env:, envFrom:, etc., not a container
			if strings.HasPrefix(trimmed, "env:") ||
				strings.HasPrefix(trimmed, "envFrom:") ||
				strings.HasPrefix(trimmed, "args:") ||
				strings.HasPrefix(trimmed, "volumeMounts:") ||
				strings.HasPrefix(trimmed, "ports:") {
				return false
			}
			// If parent is initContainers or volumes, not in containers section
			if strings.HasPrefix(trimmed, "initContainers:") ||
				strings.HasPrefix(trimmed, "volumes:") {
				return false
			}
		}
	}

	return false
}

// getIndentation returns the number of spaces at the start of a line
func getIndentation(line string) int {
	count := 0
	for _, ch := range line {
		if ch == ' ' {
			count++
		} else {
			break
		}
	}
	return count
}

// findMissingBlocks checks which blocks from the provided list are not already present in the container
func findMissingBlocks(lines []string, containerNameIndex, containerIndent int, blockStrings []string) []string {
	var missing []string

	for _, block := range blockStrings {
		if !containerHasBlock(lines, containerNameIndex, containerIndent, block) {
			missing = append(missing, block)
		}
	}

	return missing
}

// containerHasBlock checks if the container already has the specified block
func containerHasBlock(lines []string, containerNameIndex, containerIndent int, block string) bool {
	// Parse the block to get its structure
	var blockData map[string]interface{}
	if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
		return false
	}

	// Get the top-level key of the block (envFrom, resources, etc.)
	var topKey string
	for key := range blockData {
		topKey = key
		break
	}

	if topKey == "" {
		return false
	}

	// For envFrom, check if the specific configMap exists
	if topKey == "envFrom" {
		return containerHasEnvFromBlock(lines, containerNameIndex, containerIndent, blockData)
	}

	// For resources, check if resources block exists (we don't want to override existing resources)
	if topKey == "resources" {
		return containerHasResourcesBlock(lines, containerNameIndex, containerIndent)
	}

	// For other block types, check if the key exists
	return containerHasKey(lines, containerNameIndex, containerIndent, topKey)
}

// containerHasEnvFromBlock checks if the container has the specific envFrom configMap
func containerHasEnvFromBlock(lines []string, containerNameIndex, containerIndent int, blockData map[string]interface{}) bool {
	// Extract configMap name from the block
	configMapName := ""
	if envFromList, ok := blockData["envFrom"].([]interface{}); ok {
		for _, item := range envFromList {
			if itemMap, ok := item.(map[interface{}]interface{}); ok {
				if configMapRef, ok := itemMap["configMapRef"].(map[interface{}]interface{}); ok {
					if name, ok := configMapRef["name"].(string); ok {
						configMapName = name
						break
					}
				}
			}
		}
	}

	if configMapName == "" {
		return false
	}

	// Check if this configMap exists in the container's envFrom
	for i := containerNameIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if indent <= containerIndent {
			break
		}

		if indent == containerIndent+2 && strings.HasPrefix(trimmed, "envFrom:") {
			// Found envFrom, check for the configMap
			for j := i + 1; j < len(lines); j++ {
				nextLine := lines[j]
				nextTrimmed := strings.TrimSpace(nextLine)
				nextIndent := getIndentation(nextLine)

				if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
					continue
				}

				if nextIndent < containerIndent+2 {
					break
				}

				if nextTrimmed == "- configMapRef:" && j+1 < len(lines) {
					nameLine := strings.TrimSpace(lines[j+1])
					if nameLine == "name: "+configMapName {
						return true
					}
				}
			}
			break
		}
	}

	return false
}

// containerHasResourcesBlock checks if the container already has a resources block
func containerHasResourcesBlock(lines []string, containerNameIndex, containerIndent int) bool {
	return containerHasKey(lines, containerNameIndex, containerIndent, "resources")
}

// containerHasKey checks if the container has a specific top-level key
func containerHasKey(lines []string, containerNameIndex, containerIndent int, key string) bool {
	for i := containerNameIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if indent <= containerIndent {
			break
		}

		if indent == containerIndent+2 && strings.HasPrefix(trimmed, key+":") {
			return true
		}
	}
	return false
}

// findBlockInjectionPoint finds where to inject blocks in the container
func findBlockInjectionPoint(lines []string, containerNameIndex, containerIndent int) int {
	// Look for the env: block or the end of the container properties
	for i := containerNameIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := getIndentation(line)

		if indent <= containerIndent {
			return i
		}

		// Inject before env:, livenessProbe:, readinessProbe:
		if indent == containerIndent+2 && (strings.HasPrefix(trimmed, "env:") ||
			strings.HasPrefix(trimmed, "livenessProbe:") ||
			strings.HasPrefix(trimmed, "readinessProbe:")) {
			return i
		}
	}

	return len(lines)
}

// getBlocksByKey returns blocks that have the specified top-level key
func getBlocksByKey(blocks []string, key string) []string {
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

// removeBlocksByKey returns blocks that don't have the specified top-level key
func removeBlocksByKey(blocks []string, key string) []string {
	var result []string
	for _, block := range blocks {
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			continue
		}
		if _, ok := blockData[key]; !ok {
			result = append(result, block)
		}
	}
	return result
}

// findExistingKey finds the line index of a top-level key in the container, or -1 if not found
func findExistingKey(lines []string, containerNameIndex, containerIndent int, key string) int {
	for i := containerNameIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if indent <= containerIndent {
			break
		}

		if indent == containerIndent+2 && strings.HasPrefix(trimmed, key+":") {
			return i
		}
	}
	return -1
}

// findEnvFromEndPoint finds where the envFrom block ends (where to append new items)
func findEnvFromEndPoint(lines []string, envFromLineIdx, containerIndent int) int {
	for i := envFromLineIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// If we hit a property at the container level or lower, insert before it
		if indent <= containerIndent+2 {
			return i
		}
	}
	return len(lines)
}

// injectMissingBlocks injects the missing blocks with proper indentation and grouping
func injectMissingBlocks(missingBlocks []string, indent int) []string {
	spaces := strings.Repeat(" ", indent)
	var result []string

	// Group blocks by their top-level key
	grouped := make(map[string][]string)
	for _, block := range missingBlocks {
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			continue
		}

		for key := range blockData {
			grouped[key] = append(grouped[key], block)
			break
		}
	}

	// Inject envFrom blocks first (merged under single header)
	if envFromBlocks, ok := grouped["envFrom"]; ok && len(envFromBlocks) > 0 {
		result = append(result, spaces+"envFrom:")
		for _, block := range envFromBlocks {
			blockLines := strings.Split(strings.TrimSpace(block), "\n")
			// Skip the "envFrom:" line and add the rest
			for idx := 1; idx < len(blockLines); idx++ {
				result = append(result, spaces+blockLines[idx])
			}
		}
	}

	// Inject other blocks (resources, etc.)
	for key, blocks := range grouped {
		if key == "envFrom" {
			continue // Already handled
		}

		for _, block := range blocks {
			blockLines := strings.Split(strings.TrimSpace(block), "\n")
			for _, line := range blockLines {
				result = append(result, spaces+line)
			}
		}
	}

	return result
}

// generateEnvFromBlock generates the envFrom block with proper indentation
func generateEnvFromBlock(indent int) []string {
	spaces := strings.Repeat(" ", indent)
	return []string{
		spaces + "envFrom:",
		spaces + "- configMapRef:",
		spaces + "    name: inline-injector-config",
	}
}
