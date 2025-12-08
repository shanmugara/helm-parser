package helm_parser

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v2"
)

var (
	// All Pod-level configuration keys we care about
	podConfigKeys = []string{"tolerations", "affinity", "nodeSelector"}
)

// ValueReference represents a reference to a value in the values file
// e.g., .Values.tolerations or .Values.webhook.tolerations
type ValueReference struct {
	Path []string // e.g., ["tolerations"] or ["webhook", "tolerations"]
	Key  string   // e.g., "tolerations"
}

// InjectIntoValuesFile injects blocks into the values.yaml file
// It detects which sections are referenced in templates and injects accordingly
func InjectIntoValuesFile(chartDir string, blocks InjectorBlocks, referencedPaths []ValueReference, criticalDs bool, controlPlane bool) error {
	//DEBUG
	//Logger.Info("inside InjectIntoValuesFile")
	if len(referencedPaths) == 0 {
		return nil
	}

	valuesPath := filepath.Join(chartDir, "values.yaml")

	// Read the values file. This should include previous changes we made.
	content, err := os.ReadFile(valuesPath)
	if err != nil {
		return fmt.Errorf("failed to read values.yaml: %v", err)
	}

	// Detect if this uses a wrapper pattern (e.g., Istio's _internal_defaults_do_not_set)
	indentOffset := detectWrapperPattern(string(content))
	// if indentOffset > 0 {
	// 	Logger.Infof("Detected wrapper pattern with indent offset: %d", indentOffset)
	// }

	modifiedContent := string(content)
	modified := false
	//DEBUG
	//Logger.Info("referencedPaths:", referencedPaths)

	// Process each referenced path
	for _, ref := range referencedPaths {
		var injectedBlocks []string

		// Determine which blocks to inject based on the key
		// First check if it's a pod-level key
		if slices.Contains(podConfigKeys, ref.Key) {
			// Pod-level blocks
			switch ref.Key {
			case "tolerations":
				injectedBlocks = getPodBlocksByKey(blocks["allPods"], "tolerations")
				if criticalDs {
					critDsBlocks := getPodBlocksByKey(blocks["criticalDsPods"], "tolerations")
					injectedBlocks = append(injectedBlocks, critDsBlocks...)
				}
				if controlPlane {
					cpBlocks := getPodBlocksByKey(blocks["controlPlanePods"], "tolerations")
					injectedBlocks = append(injectedBlocks, cpBlocks...)
				}
			case "affinity":
				injectedBlocks = getPodBlocksByKey(blocks["allPods"], "affinity")
				if criticalDs {
					critDsBlocks := getPodBlocksByKey(blocks["criticalDsPods"], "affinity")
					injectedBlocks = append(injectedBlocks, critDsBlocks...)
				}
				if controlPlane {
					cpBlocks := getPodBlocksByKey(blocks["controlPlanePods"], "affinity")
					injectedBlocks = append(injectedBlocks, cpBlocks...)
				}
			case "nodeSelector":
				injectedBlocks = getPodBlocksByKey(blocks["allPods"], "nodeSelector")
			}
		} else {
			// Container-level blocks - dynamically check all container blocks
			injectedBlocks = getContainerBlocksByKey(blocks["allContainers"], ref.Key)
			// If no blocks found, skip this key
			if len(injectedBlocks) == 0 {
				continue
			}
		}

		if len(injectedBlocks) > 0 {
			newContent, changed, actuallyInjected := injectBlockIntoValuesPath(modifiedContent, ref, injectedBlocks, indentOffset)
			if changed {
				modifiedContent = newContent
				modified = true
				if actuallyInjected {
					Logger.Infof("Injected %s into values at path: %v", ref.Key, ref.Path)
				}
			}
		}
	}

	if modified {
		if err := os.WriteFile(valuesPath, []byte(modifiedContent), 0644); err != nil {
			return fmt.Errorf("failed to write updated values.yaml: %v", err)
		}
		Logger.Infof("Updated values.yaml with injected blocks")
	}

	return nil
}

// injectBlockIntoValuesPath injects blocks into a specific path in values.yaml
// This works for any nested structure, e.g., ["tolerations"] or ["webhook", "tolerations"]
// indentOffset handles wrapper patterns like _internal_defaults_do_not_set
// Returns: (newContent, fileModified, actuallyInjected)
func injectBlockIntoValuesPath(content string, ref ValueReference, blocks []string, indentOffset int) (string, bool, bool) {
	lines := strings.Split(content, "\n")
	var result []string
	modified := false
	actuallyInjected := false

	// Build current path as we traverse the file
	type PathLevel struct {
		indent int
		key    string
	}
	var pathStack []PathLevel

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		actualIndent := getIndentation(line)

		// Skip empty lines and comments - add to result and continue
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			result = append(result, line)
			continue
		}

		// Check if this is the wrapper line itself - preserve it and skip processing
		if indentOffset > 0 && actualIndent == 0 && strings.Contains(trimmed, ":") {
			key := strings.TrimSpace(strings.Split(trimmed, ":")[0])
			if slices.Contains(KnownWrapperKeys, key) {
				result = append(result, line)
				continue
			}
		}

		// Apply virtual indent offset - treat wrapper content as if at higher root
		indent := actualIndent
		if indentOffset > 0 && actualIndent >= indentOffset {
			indent = actualIndent - indentOffset
		}

		// Update path stack based on virtual indentation
		// Pop entries from stack if we've outdented
		for len(pathStack) > 0 && pathStack[len(pathStack)-1].indent >= indent {
			pathStack = pathStack[:len(pathStack)-1]
		}

		// Extract key from line if it's a key:value line
		if strings.Contains(trimmed, ":") {
			key := strings.TrimSpace(strings.Split(trimmed, ":")[0])

			// Add to path stack
			pathStack = append(pathStack, PathLevel{indent: indent, key: key})

			// Build current path from stack
			currentPath := make([]string, len(pathStack))
			for j, level := range pathStack {
				currentPath[j] = level.key
			}

			// Check if we've reached the target path
			if pathsMatch(currentPath, ref.Path) {
				// Found it! Now inject
				value := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
				isEmpty := value == "" || value == "[]" || value == "{}"

				if isEmpty {
					// Empty inline value - but check if there's content on subsequent lines
					// For complex nested structures (not list-based), check for existing content
					if key == "affinity" || key == "tolerations" || isComplexNestedBlock(key, blocks) {
						j := i + 1
						hasExistingContent := false
						for j < len(lines) {
							nextLine := lines[j]
							nextIndent := getIndentation(nextLine)
							nextTrimmed := strings.TrimSpace(nextLine)

							if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
								j++
								continue
							}

							if nextIndent <= actualIndent {
								break
							}

							// Found content at higher indent - already has content
							hasExistingContent = true
							break
						}

						if hasExistingContent {
							// Already has content - behavior depends on block type

							if key == "tolerations" {
								// For tolerations, collect existing and only add new ones (merge behavior)
								result = append(result, line)
								var existingContent []string
								j = i + 1
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
									existingContent = append(existingContent, nextTrimmed)
									j++
								}

								// Check if blocks to inject already exist
								injected := injectBlockLines(blocks, actualIndent+2, key)
								for _, injLine := range injected {
									injTrimmed := strings.TrimSpace(injLine)
									if !containsLine(existingContent, injTrimmed) {
										result = append(result, injLine)
										modified = true
										actuallyInjected = true
									}
								}
								i = j - 1
								continue
							} else if key == "affinity" || isComplexNestedBlock(key, blocks) {
								// For complex nested structures (affinity, resources, etc.), replace existing content
								// Skip the existing content
								j = i + 1
								for j < len(lines) {
									nextLine := lines[j]
									nextIndent := getIndentation(nextLine)
									nextTrimmed := strings.TrimSpace(nextLine)

									if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
										j++
										continue
									}

									if nextIndent <= actualIndent {
										break
									}
									// Skip this line (don't add to result)
									j++
								}

								// Now inject our new content
								result = append(result, strings.Repeat(" ", actualIndent)+key+":")
								injected := injectBlockLines(blocks, actualIndent+2, key)
								result = append(result, injected...)
								modified = true
								actuallyInjected = true

								i = j - 1
								continue
							}
						}
					}

					// Empty value and no existing content - inject our blocks
					// Use actualIndent for writing back to preserve file structure
					result = append(result, strings.Repeat(" ", actualIndent)+key+":")
					injected := injectBlockLines(blocks, actualIndent+2, key)
					result = append(result, injected...)
					modified = true
					actuallyInjected = true

					// Skip original value line if it was inline []  or {}
					if value == "[]" || value == "{}" {
						continue
					}
				} else {
					// Has existing content - check if our content already exists
					result = append(result, line)

					// For tolerations, check if blocks already exist before appending
					if key == "tolerations" {
						// Collect existing tolerations content
						var existingContent []string
						j := i + 1
						for j < len(lines) {
							nextLine := lines[j]
							nextIndent := getIndentation(nextLine)
							nextTrimmed := strings.TrimSpace(nextLine)

							if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
								result = append(result, nextLine)
								j++
								continue
							}

							// Use actualIndent for comparison to respect file structure
							if nextIndent <= actualIndent {
								break
							}

							result = append(result, nextLine)
							existingContent = append(existingContent, nextTrimmed)
							j++
						}

						// Check if blocks to inject already exist
						injected := injectBlockLines(blocks, actualIndent+2, key)
						for _, injLine := range injected {
							injTrimmed := strings.TrimSpace(injLine)
							if !containsLine(existingContent, injTrimmed) {
								result = append(result, injLine)
								modified = true
								actuallyInjected = true
							}
						}
						i = j - 1
						continue
					} else if key == "affinity" || isComplexNestedBlock(key, blocks) {
						// For complex nested structures, if there's already content, skip injection entirely
						// These structures are complex - don't try to merge
						j := i + 1
						hasExistingContent := false
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
							hasExistingContent = true
							j++
						}

						// Only inject if there's no existing content
						if !hasExistingContent {
							injected := injectBlockLines(blocks, actualIndent+2, key)
							result = append(result, injected...)
							modified = true
							actuallyInjected = true
						}
						i = j - 1
						continue
					} else if key == "tolerations" || isListBasedBlock(key, blocks) {
						// For list-based blocks (tolerations, envFrom, env), append new items if not already present
						var existingContent []string
						j := i + 1
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
							existingContent = append(existingContent, nextTrimmed)
							j++
						}

						// Check if blocks to inject already exist
						injected := injectBlockLines(blocks, actualIndent+2, key)
						for _, injLine := range injected {
							injTrimmed := strings.TrimSpace(injLine)
							if !containsLine(existingContent, injTrimmed) {
								result = append(result, injLine)
								modified = true
								actuallyInjected = true
							}
						}
						i = j - 1
						continue
					}
				}
				continue
			}
		}

		result = append(result, line)
	}

	// If we didn't find the path and inject, check if it's a root-level key that needs to be added
	if !actuallyInjected && len(ref.Path) == 1 {
		// Root-level key doesn't exist - append it at the end
		key := ref.Path[0]

		// Add a blank line before the new section if file doesn't end with blank line
		if len(result) > 0 && strings.TrimSpace(result[len(result)-1]) != "" {
			result = append(result, "")
		}

		// Add the new key and its blocks
		result = append(result, key+":")
		injected := injectBlockLines(blocks, 2+indentOffset, key)
		result = append(result, injected...)
		modified = true
		actuallyInjected = true
	}

	return strings.Join(result, "\n"), modified, actuallyInjected
}

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
