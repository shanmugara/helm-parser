package helm_parser

import (
	"fmt"
	"slices"
	"strings"
)

var (
	// All Pod-level configuration keys we care about
	podConfigKeys = []string{"annotations", "tolerations", "affinity", "nodeSelector", "priorityClassName"}
	// All Container-level configuration keys we care about we care about
	containerConfigKeys = []string{"resources", "env", "envFrom", "volumeMounts"}
	// All Service-level configuration keys we care about
	serviceConfigKeys = []string{"type"}
)

// InjectIntoValuesFile injects blocks into the values.yaml file
// It detects which sections are referenced in templates and injects accordingly
// blocks: full yaml string of the injector yaml file parsed into map
// referencedPaths: list of ValueReference detected in templates
func InjectIntoValuesFile(chartDir string, blocks InjectorBlocks, referencedPaths []ValueReference, criticalDs bool, controlPlane bool) error {
	blockKeys := []string{}
	//blocks["newValues"], blocks["allPods"], blocks["allContainers"], blocks["serviceSpec"])..etc
	for k := range blocks {
		blockKeys = append(blockKeys, k)
	}
	// Logger.Infof("DEBUG InjectIntoValuesFile: called with %d referencedPaths, blocks keys: %v", len(referencedPaths), blockKeys)
	if len(referencedPaths) == 0 {
		// Logger.Infof("DEBUG InjectIntoValuesFile: No work to do, returning")
		return nil
	}
	// Read existing values.yaml
	valuesContent, err := readValuesFile(chartDir)
	if err != nil {
		return fmt.Errorf("failed to read values.yaml: %v", err)
	}

	// Detect if this uses a wrapper pattern (e.g., Istio's _internal_defaults_do_not_set)
	indentOffset := detectWrapperPattern(string(valuesContent))
	// start with original content
	modifiedContent := string(valuesContent)
	modified := false

	// Process each referenced path
	for _, ref := range referencedPaths {
		var injectedBlocks []string
		// Determine which blocks to inject based on the key
		// First check if it's a pod-level key
		if slices.Contains(podConfigKeys, ref.Key) {
			// Pod-level blocks
			// We need to add new keys as we go, so handle each key specifically
			// these keys are based on our current customizations as documented in kubception-docs
			switch ref.Key {
			case "tolerations", "affinity", "annotations":
				injectedBlocks = collectPodBlocks(blocks, ref.Key, criticalDs, controlPlane)
			case "nodeSelector":
				injectedBlocks = getPodBlocksByKey(blocks["allPods"], ref.Key)
			case "priorityClassName":
				injectedBlocks = getPodBlocksByKey(blocks["allPods"], ref.Key)
			}
		} else if slices.Contains(containerConfigKeys, ref.Key) {
			// Container-level blocks - dynamically check all container blocks
			injectedBlocks = getContainerBlocksByKey(blocks["allContainers"], ref.Key)
			// If no blocks found, skip this key
			if len(injectedBlocks) == 0 {
				continue
			}
		} else if slices.Contains(serviceConfigKeys, ref.Key) {
			// Service-level blocks
			injectedBlocks = getServiceBlocksByKey(blocks["serviceSpec"], ref.Key)
			if len(injectedBlocks) == 0 {
				continue
			}
		}

		// Add more cases as needed in future

		// Inject the blocks into the values file at the specified path
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

	// Inject custom newValues from inject-blocks.yaml at the root level
	// these are vlaues we need to add to customize the chart, not part of teh default chart vlaues

	if modified {
		if err := writeValuesFile(chartDir, []byte(modifiedContent)); err != nil {
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
	pathStack := NewPathStack()

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		yl := ParseLine(line)
		//actualIndent := yl.Indent

		// Skip empty lines and comments - add to result and continue
		if yl.IsEmpty || yl.IsComment {
			result = append(result, line)
			continue
		}

		// Check if this is the wrapper line itself - preserve it and skip processing
		//DEBUG
		Logger.Debugf("DEBUG injectBlockIntoValuesPath: Processing line %d: '%s' (indentOffset=%d, key=%s, hasColon=%v)", i, line, indentOffset, yl.Key, yl.HasColon)

		if indentOffset > 0 && yl.Indent == 0 && yl.HasColon {
			if slices.Contains(KnownWrapperKeys, yl.Key) {
				result = append(result, line)
				continue
			}
		}

		// Apply virtual indent offset - treat wrapper content as if at higher root
		indent := yl.Indent
		if indentOffset > 0 && yl.Indent >= indentOffset {
			indent = yl.Indent - indentOffset
		}

		// Update path stack based on virtual indentation
		pathStack.PopToIndent(indent)

		// Extract key from line if it's a key:value line
		if yl.HasColon {
			// Add to path stack
			pathStack.Push(indent, yl.Key)

			// Build current path from stack
			currentPath := pathStack.CurrentPath()

			// Check if we've reached the target path
			if pathsMatch(currentPath, ref.Path) {
				Logger.Debugf("DEBUG: Matched target path %v for key %s", ref.Path, yl.Key)
				// Found it! Now inject
				isEmpty := yl.Value == "" || yl.Value == "[]" || yl.Value == "{}"

				if isEmpty {
					// Empty inline value - but check if there's content on subsequent lines
					// For complex nested structures (not list-based), check for existing content
					isComplexNested := isComplexNestedBlock(yl.Key, blocks)

					if slices.Contains(podConfigKeys, yl.Key) || isComplexNested {
						Logger.Debugf("DEBUG: Checking for existing content for complex key=%s since isEmpty:%v", yl.Key, isEmpty)
						j := i + 1
						hasExistingContent := false
						for j < len(lines) {
							nextLine := lines[j]
							nextIndent := getIndentation(nextLine)
							nextTrimmed := strings.TrimSpace(nextLine)

							//DEBUG
							Logger.Debugf("DEBUG: Checking \nline j=%d\nnextLine:'%s' (nextIndent=%d, currentIndent=%d)\ncurrent line:%s", j, nextLine, nextIndent, yl.Indent, line)
							// Pause for debugging
							// if yl.Key == "tolerations" {
							// 	fmt.Println("Debug Press Enter to continue...")
							// 	fmt.Scanln()
							// }

							if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
								// Skip empty lines and comments
								j++
								continue
							}

							// add a logic to check if nextIndent == yl.Indent and if the nextLineTrimmed starts with '- '
							// this indicates that the current key is a list and has existing content
							if nextIndent == yl.Indent && strings.HasPrefix(nextTrimmed, "- ") {
								hasExistingContent = true
								Logger.Debugf("DEBUG: Found existing list content for key=%s at line j=%d: %s", yl.Key, j, nextTrimmed)
								break
							}

							if nextIndent <= yl.Indent {
								Logger.Debugf("DEBUG: Stopped checking nextIndent <= yl.Indent for \nkey=%s nextIndent=%d yl.Indent:%d", yl.Key, nextIndent, yl.Indent)
								// fmt.Println("Press Enter to continue...")
								// fmt.Scanln()
								// Reached same or higher level - no content found
								break
							}

							// Found content at higher indent - already has content
							hasExistingContent = true
							Logger.Debugf("DEBUG: Found existing content for key=%s at line j=%d: %s", yl.Key, j, nextTrimmed)
							break
						}
						Logger.Debugf("DEBUG: Final value for hasExistingContent for key=%s: %v", yl.Key, hasExistingContent)

						if hasExistingContent {
							// Already has content - behavior depends on block type
							Logger.Debugf("DEBUG injectBlockIntoValuesPath: Key %s has existing content, checking handler", yl.Key)
							if yl.Key == "tolerations" {
								Logger.Debugf("DEBUG: Calling mergeTolerations for key=tolerations")
								var modifiedTol, injectedTol bool
								var j int
								//lines: values.yaml lines
								//result: current result lines should contain only upto wrapper key if any
								//i: current line index in values.yaml
								//yl.Indent: indent of current line
								//blocks: blocks to inject
								//line: current line content
								//fmt.Println("result before mergeTolerations:\n%+v", strings.Join(result, "\n"))
								//fmt.Println("Press Enter to continue...")
								//fmt.Scanln()
								result, j, modifiedTol, injectedTol = mergeTolerations(lines, result, i, yl.Indent, blocks, line)
								modified = modified || modifiedTol
								actuallyInjected = actuallyInjected || injectedTol
								i = j - 1
								continue
							} else if yl.Key == "affinity" || isComplexNestedBlock(yl.Key, blocks) {
								var modifiedComplex, injectedComplex bool
								var j int
								result, j, modifiedComplex, injectedComplex = handleComplexNestedBlock(lines, result, i, yl.Indent, blocks, yl, true)
								modified = modified || modifiedComplex
								actuallyInjected = actuallyInjected || injectedComplex
								i = j - 1
								continue
							}
						}
					}

					// Empty value and no existing content - inject our blocks
					// Use actualIndent for writing back to preserve file structure
					result = append(result, strings.Repeat(" ", yl.Indent)+yl.Key+":")
					//injected := injectBlockLines(blocks, 2+yl.Indent, yl.Key) 12-25-2025 remove +2 indent
					indentAdd := 2
					// Special case: for tolerations, keep same indent as key for list items
					if yl.Key == "tolerations" {
						indentAdd = 0
					}
					injected := injectBlockLines(blocks, indentAdd+yl.Indent, yl.Key)
					result = append(result, injected...)
					modified = true
					actuallyInjected = true

					// Skip original value line if it was inline []  or {}
					if yl.Value == "[]" || yl.Value == "{}" {
						continue
					}
				} else {
					// Has existing content - check if our content already exists

					// For simple scalar values (like priorityClassName: "value"), replace the entire line
					if !isComplexNestedBlock(yl.Key, blocks) && !isListBasedBlock(yl.Key, blocks) {
						Logger.Infof("Found scalar value for key %s:%s, replacing with injected content", yl.Key, yl.Value)
						// Simple scalar value - replace with injected content
						injected := injectBlockLines(blocks, yl.Indent, yl.Key)
						if len(injected) > 0 {
							result = append(result, injected...)
							modified = true
							actuallyInjected = true
							Logger.Infof("Replaced scalar value for key=%s", yl.Key)
						} else {
							result = append(result, line)
						}
						continue
					}

					result = append(result, line)

					// For tolerations, check if blocks already exist before appending
					if yl.Key == "tolerations" {
						var modifiedTol, injectedTol bool
						var j int
						result, j, modifiedTol, injectedTol = mergeTolerations(lines, result, i, yl.Indent, blocks, line)
						modified = modified || modifiedTol
						actuallyInjected = actuallyInjected || injectedTol
						i = j - 1
						continue
					} else if yl.Key == "affinity" || isComplexNestedBlock(yl.Key, blocks) {
						var modifiedComplex, injectedComplex bool
						var j int
						result, j, modifiedComplex, injectedComplex = handleComplexNestedBlock(lines, result, i, yl.Indent, blocks, yl, false)
						modified = modified || modifiedComplex
						actuallyInjected = actuallyInjected || injectedComplex
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
		// If there's a wrapper pattern, indent the key appropriately
		keyIndent := indentOffset

		// Check if this is a single-line scalar value (like "priorityClassName: value")
		if isSingleLineScalar(blocks, key) {
			// For single-line scalars, the block already contains the key, so just add it directly
			injected := injectBlockLines(blocks, keyIndent, key)
			result = append(result, injected...)
		} else {
			// For multi-line blocks, add the key line first, then the content
			result = append(result, strings.Repeat(" ", keyIndent)+key+":")
			injected := injectBlockLines(blocks, 2+indentOffset, key)
			result = append(result, injected...)
		}

		modified = true
		actuallyInjected = true
		Logger.Infof("Added new root-level key '%s' with indentOffset=%d", key, indentOffset)
	}

	return strings.Join(result, "\n"), modified, actuallyInjected
}

// findRootKey checks if a key exists at the root level (accounting for wrapper patterns)
// Returns: (keyExists, valuePreview)
func findRootKey(lines []string, targetKey string, indentOffset int) (bool, string) {
	pathStack := NewPathStack()

	for _, line := range lines {
		yl := ParseLine(line)

		// Skip empty lines and comments
		if yl.IsEmpty || yl.IsComment {
			continue
		}

		// Skip wrapper line itself
		if indentOffset > 0 && yl.Indent == 0 && yl.HasColon {
			if slices.Contains(KnownWrapperKeys, yl.Key) {
				continue
			}
		}

		// Apply virtual indent offset
		indent := yl.Indent
		if indentOffset > 0 && yl.Indent >= indentOffset {
			indent = yl.Indent - indentOffset
		}

		// Update path stack based on virtual indentation
		pathStack.PopToIndent(indent)

		// Extract key from line if it's a key:value line
		if yl.HasColon {
			// Add to path stack
			pathStack.Push(indent, yl.Key)

			// Check if we're at root level (path length 1) and key matches
			currentPath := pathStack.CurrentPath()
			if len(currentPath) == 1 && currentPath[0] == targetKey {
				return true, yl.Value
			}
		}
	}

	return false, ""
}

// findWrapperEndPosition finds the position where new content should be inserted
// when a wrapper pattern exists. It returns the line number after the last content
// line that belongs to the wrapper (but before any trailing empty lines or comments at the end).
func findWrapperEndPosition(lines []string, indentOffset int) int {
	lastContentLine := -1
	foundWrapper := false
	wrapperLine := -1

	for i, line := range lines {
		yl := ParseLine(line)

		// Skip empty lines and comments for now, but remember them
		if yl.IsEmpty || yl.IsComment {
			continue
		}

		// Check if this is the wrapper line itself
		if yl.Indent == 0 && yl.HasColon && slices.Contains(KnownWrapperKeys, yl.Key) {
			foundWrapper = true
			wrapperLine = i
			continue
		}

		// If we found the wrapper and this line is indented at or below wrapper level (0),
		// and it's not the wrapper itself, we've gone past the wrapper content
		// this should not happen as usually there is only one root-level wrapper - safety net
		if foundWrapper && yl.Indent == 0 {
			break
		}

		// If we're inside the wrapper (indent >= indentOffset) or haven't found wrapper yet,
		// mark this as last content line
		if foundWrapper {
			if yl.Indent >= indentOffset {
				lastContentLine = i
			} else if yl.Indent == 0 {
				// Hit a root-level key that's not the wrapper, stop here
				// again this should not happen - safety net
				break
			}
		}
	}

	// If we found content, insert after the last content line
	// Otherwise, insert at the end of file
	if lastContentLine >= 0 {
		return lastContentLine + 1
	}

	// If no content found but wrapper exists, find the wrapper line and insert after it
	if foundWrapper {
		return wrapperLine
	}

	// Default to end of file
	return len(lines)
}

// deepMergeYAML recursively merges source map into destination map
// For conflicting keys: source values take precedence
// For nested maps: recursively merge
// Returns a new merged map
func deepMergeYAML(existingMap, newMap map[interface{}]interface{}) map[interface{}]interface{} {
	result := make(map[interface{}]interface{})

	// Copy all keys from existingMap
	for k, v := range existingMap {
		result[k] = v
	}

	// Merge in keys from newMap
	for k, srcValue := range newMap {
		// Check if key exists in existing map
		if destValue, exists := result[k]; exists {
			// Key exists in both - check if both are maps
			if destMap, destIsMap := destValue.(map[interface{}]interface{}); destIsMap {
				if srcMap, srcIsMap := srcValue.(map[interface{}]interface{}); srcIsMap {
					// Both are maps - recursive merge
					result[k] = deepMergeYAML(destMap, srcMap)
					continue
				}
			}
			// One or both are not maps, or different types - newMap overwrites
			result[k] = srcValue
		} else {
			// Key only exists in newMap - add it
			result[k] = srcValue
		}
	}

	return result
}

// toInterfaceMap converts various map types to map[interface{}]interface{}
// Returns (converted map, true) if successful, (nil, false) otherwise
func toInterfaceMap(v interface{}) (map[interface{}]interface{}, bool) {
	switch m := v.(type) {
	case map[interface{}]interface{}:
		return m, true
	case map[string]interface{}:
		result := make(map[interface{}]interface{})
		for k, val := range m {
			result[k] = val
		}
		return result, true
	default:
		return nil, false
	}
}
