package helm_parser

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var (
	// All Pod-level configuration keys we care about
	podConfigKeys = []string{"tolerations", "affinity", "nodeSelector", "priorityClassName"}
	// All Container-level configuration keys we care about we care about
	containerConfigKeys = []string{"resources", "env", "envFrom", "volumeMounts"}
)

// InjectIntoValuesFile injects blocks into the values.yaml file
// It detects which sections are referenced in templates and injects accordingly
func InjectIntoValuesFile(chartDir string, blocks InjectorBlocks, referencedPaths []ValueReference, criticalDs bool, controlPlane bool, systemCritical string) error {
	//DEBUG
	//Logger.Info("inside InjectIntoValuesFile")
	if len(referencedPaths) == 0 {
		return nil
	}

	valuesPath := filepath.Join(chartDir, "values.yaml")

	// Read the values file. This should include previous changes we made in registry updates.
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
			// We need to add new keys as we go, so handle each key specifically
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

			case "priorityClassName":
				//DEBUG
				//Logger.Infof("injected blocks before priorityClass: %v", injectedBlocks)

				injectedBlocks = getPodBlocksByKey(blocks["allPods"], "priorityClassName")

				//DEBUG
				// Logger.Infof("injected blocks after allPods priorityClass: %v", injectedBlocks)
				// fmt.Println("Press 'Enter' to continue...")
				// fmt.Scanln()

				// 	Logger.Infof("Processing priorityClassName with systemCritical=%s", systemCritical)
				// 	switch systemCritical {
				// 	case "node":
				// 		injectedBlocks = getBlocksByKey(blocks["systemCriticalNodePods"], "priorityClassName")
				// 		Logger.Infof("node: found %d blocks", len(injectedBlocks))
				// 	case "cluster":
				// 		injectedBlocks = getBlocksByKey(blocks["systemCriticalClusterPods"], "priorityClassName")
				// 		Logger.Infof("cluster: found %d blocks", len(injectedBlocks))
				// 	default:
				// 		injectedBlocks = getBlocksByKey(blocks["systemCriticalDefaultPods"], "priorityClassName")
				// 		Logger.Infof("default: found %d blocks", len(injectedBlocks))
				// 		// Non-system-critical pods get no priorityClassName injection
				// 	}
			}
		} else if slices.Contains(containerConfigKeys, ref.Key) {
			// Container-level blocks - dynamically check all container blocks
			injectedBlocks = getContainerBlocksByKey(blocks["allContainers"], ref.Key)
			// If no blocks found, skip this key
			if len(injectedBlocks) == 0 {
				continue
			}
		}
		// Add more cases as needed in future

		//DEBUG
		// Logger.Infof("Injected blocks: %v", injectedBlocks)
		// //add puse and press any key to continue
		// fmt.Println("Press 'Enter' to continue...")
		// fmt.Scanln()

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
	pathStack := NewPathStack()

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		yl := ParseLine(line)
		actualIndent := yl.Indent

		// Skip empty lines and comments - add to result and continue
		if yl.IsEmpty || yl.IsComment {
			result = append(result, line)
			continue
		}

		// Check if this is the wrapper line itself - preserve it and skip processing
		if indentOffset > 0 && actualIndent == 0 && yl.HasColon {
			if slices.Contains(KnownWrapperKeys, yl.Key) {
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
		pathStack.PopToIndent(indent)

		// Extract key from line if it's a key:value line
		if yl.HasColon {
			// Add to path stack
			pathStack.Push(indent, yl.Key)

			// Build current path from stack
			currentPath := pathStack.CurrentPath()

			// Check if we've reached the target path
			if pathsMatch(currentPath, ref.Path) {
				// Found it! Now inject
				isEmpty := yl.Value == "" || yl.Value == "[]" || yl.Value == "{}"

				if isEmpty {
					// Empty inline value - but check if there's content on subsequent lines
					// For complex nested structures (not list-based), check for existing content

					if slices.Contains(podConfigKeys, yl.Key) || isComplexNestedBlock(yl.Key, blocks) {
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

							if yl.Key == "tolerations" {
								// For tolerations, collect existing and only add new ones (merge behavior)
								// append the key line first ("tolerations:", "affinity:", etc.)
								result = append(result, line)
								var existingContent []string
								j = i + 1
								for j < len(lines) {
									nextLine := lines[j]
									nextIndent := getIndentation(nextLine)
									nextTrimmed := strings.TrimSpace(nextLine)
									// inject empty lines and comments as-is
									if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
										result = append(result, nextLine)
										j++
										continue
									}
									// if the indent is less than or equal to actualIndent, we've reached end of this block
									if nextIndent <= actualIndent {
										break
									}

									result = append(result, nextLine)
									existingContent = append(existingContent, nextTrimmed)
									j++
								}

								// Check if blocks to inject already exist
								injected := injectBlockLines(blocks, actualIndent+2, yl.Key)
								for _, injLine := range injected {
									injTrimmed := strings.TrimSpace(injLine)
									if !containsLine(existingContent, injTrimmed) {
										result = append(result, injLine)
										modified = true
										actuallyInjected = true
									}
								}
								// Mark as injected even if all blocks already exist
								actuallyInjected = true
								i = j - 1
								continue
							} else if yl.Key == "affinity" || isComplexNestedBlock(yl.Key, blocks) {
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
									// if the indent is less than or equal to actualIndent, we've reached end of this block
									if nextIndent <= actualIndent {
										break
									}
									j++
								}

								// Now inject our new content
								result = append(result, strings.Repeat(" ", actualIndent)+yl.Key+":")
								injected := injectBlockLines(blocks, actualIndent+2, yl.Key)
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
					result = append(result, strings.Repeat(" ", actualIndent)+yl.Key+":")
					injected := injectBlockLines(blocks, actualIndent+2, yl.Key)
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
						injected := injectBlockLines(blocks, actualIndent, yl.Key)
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

						//TO BE CONTINUED FROM HERE -------->>>>>>>>>>>>>>>> code duplication
						// Check if blocks to inject already exist
						injected := injectBlockLines(blocks, actualIndent+2, yl.Key)
						for _, injLine := range injected {
							injTrimmed := strings.TrimSpace(injLine)
							if !containsLine(existingContent, injTrimmed) {
								result = append(result, injLine)
								modified = true
								actuallyInjected = true
							}
						}
						// Mark as injected even if nothing was added (found existing tolerations)
						actuallyInjected = true
						i = j - 1
						continue
					} else if yl.Key == "affinity" || isComplexNestedBlock(yl.Key, blocks) {
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
							injected := injectBlockLines(blocks, actualIndent+2, yl.Key)
							result = append(result, injected...)
							modified = true
							actuallyInjected = true
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
