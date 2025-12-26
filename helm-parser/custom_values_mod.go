package helm_parser

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v2"
)

func ApplyCustomValuesMods(chartDir string, customYaml string) error {
	// Placeholder for future implementation of custom values modifications
	blocks, err := loadInjectorBlocks(customYaml)
	if err != nil {
		return fmt.Errorf("failed to load injector blocks during ApplyCustomValuesMods: %v", err)
	}
	if len(blocks["newValues"]) > 0 {
		err := InjectNewValuesOnly(chartDir, blocks["newValues"])
		if err != nil {
			return fmt.Errorf("failed to inject new values during ApplyCustomValuesMods: %v", err)
		}
	} else {
		Logger.Info("No custom newValues found... skipping")
	}

	return nil
}

// this injects only the newValues blocks into values.yaml
func InjectNewValuesOnly(chartDir string, newValueBlocks []string) error {
	// Read existing values.yaml
	content, err := readValuesFile(chartDir)
	if err != nil {
		return fmt.Errorf("failed to read values.yaml: %v", err)
	}

	// Detect wrapper pattern
	indentOffset := detectWrapperPattern(string(content))

	// Inject newValues
	Logger.Debugf("DEBUG: InjectNewValuesOnly About to inject %d newValues blocks (pre-render)", len(newValueBlocks))
	Logger.Debugf("DEBUG: InjectNewVlauesOnly Injecting \n%+v newValues blocks", newValueBlocks)
	//fmt.Println("Press Enter to continue...")
	//fmt.Scanln()

	// newValues are always injected at root level, so make sure to
	// specify the newValues block correctly with proper indentation
	newContent, changed := injectNewValuesIntoRoot(string(content), newValueBlocks, indentOffset)
	if changed {
		if err := writeValuesFile(chartDir, []byte(newContent)); err != nil {
			return fmt.Errorf("failed to write updated values.yaml: %v", err)
		}
		Logger.Infof("InjectNewValuesOnly Injected newValues into root of values.yaml (pre-render)")
	}

	return nil
}

// injectNewValuesIntoRoot injects new key-value pairs from newValues blocks into the root level of values.yaml
// It performs deep merge to preserve existing nested keys while adding/updating new ones
// indentOffset handles wrapper patterns like _internal_defaults_do_not_set
// content: modified content for values.yaml
// newValuesBlocks: list of newValues inject at root
// Returns: (newContent, fileModified)
func injectNewValuesIntoRoot(content string, newValuesBlocks []string, indentOffset int) (string, bool) {
	lines := strings.Split(content, "\n")
	modified := false

	// Parse each block to extract the keys from newValues
	// read each block and process
	for blockIdx, block := range newValuesBlocks {
		var blockData map[string]interface{}
		if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
			Logger.Errorf("Failed to unmarshal newValues block %d: %v", blockIdx, err)
			continue
		}

		// Each block should have exactly one key at the root level
		for key, newValue := range blockData {
			Logger.Debugf("DEBUG: injectNewValuesIntoRoot Injecting %d:\n%s: %v", blockIdx, key, newValue)
			//fmt.Scanln()
			// Check if the key already exists in the values file
			keyExists, existingValue, startLine, endLine := findAndRemoveRootKey(lines, key, indentOffset)
			Logger.Debugf("DEBUG: injectNewValuesIntoRoot key '%s' exists: %v (lines %d-%d)", key, keyExists, startLine, endLine)
			//fmt.Scanln()

			// define a variable to hold the final block content to inject
			finalBlock := block
			// If the key exists, perform deep merge if both existing and new values are maps
			//global:				# Line 0
			//  image: nginx 		# Line 1
			//app:      			# Line 2
			//  replicas: 3 		# Line 3
			//  resources:      	# Line 4
			//    cpu: 100m        	# Line 5
			//    memory: 256Mi    	# Line 6
			//service:        		# Line 7
			//  port: 80        	# Line 8

			if keyExists {
				// Extract the full YAML content for the existing key
				existingContent := strings.Join(lines[startLine:endLine+1], "\n")
				// for app:
				// strings.Join(lines[startLine:endLine+1], "\n")
				// range lines[2:7]
				// Returns: ["app:", "  replicas: 3", "  resources:", "    cpu: 100m", "    memory: 256Mi"]
				// existingContent Returns:
				// "app:
				//   replicas: 3
				//   resources:
				//     cpu: 100m
				//     memory: 256Mi"
				Logger.Debugf("DEBUG: injectNewValuesIntoRoot Existing content for key '%s' (lines %d-%d, %d total lines)", key, startLine, endLine, len(lines[startLine:endLine+1]))
				// Log first and last few lines to diagnose extraction issues
				//extractedLines := lines[startLine : endLine+1]
				//if len(extractedLines) > 10 {
				//	Logger.Debugf("DEBUG: injectNewValuesIntoRoot First 5 lines:\n%s", strings.Join(extractedLines[:5], "\n"))
				//	Logger.Debugf("DEBUG: injectNewValuesIntoRoot Last 5 lines:\n%s", strings.Join(extractedLines[len(extractedLines)-5:], "\n"))
				//} else {
				//	Logger.Infof("DEBUG: injectNewValuesIntoRoot Extracted content:\n%s", existingContent)
				//}

				// Parse the existing content
				var existingData map[string]interface{}
				if err := yaml.Unmarshal([]byte(existingContent), &existingData); err == nil {
					Logger.Debugf("DEBUG: Parsed existingData: %+v", existingData)
					// Successfully parsed existing content as YAML
					if existingVal, hasKey := existingData[key]; hasKey {
						Logger.Debugf("DEBUG: Found key '%s' in existingData, value type: %T", key, existingVal)
						// Check if both existing and new values are maps
						if existingMap, ok := toInterfaceMap(existingVal); ok {
							Logger.Debugf("DEBUG: Existing value is a map with %d keys", len(existingMap))
							if newMap, ok := toInterfaceMap(newValue); ok {
								Logger.Debugf("DEBUG: New value is a map with %d keys", len(newMap))
								// Deep merge: merge new values into existing map
								mergedValue := deepMergeYAML(existingMap, newMap)
								Logger.Debugf("DEBUG: Merged value has %d keys", len(mergedValue))
								Logger.Infof("injectNewValuesIntoRoot: key '%s' already exists, performing deep merge", key)

								// Re-marshal the merged block
								mergedBlock, err := yaml.Marshal(map[string]interface{}{key: mergedValue})
								if err != nil {
									Logger.Errorf("Failed to marshal merged value for key '%s': %v", key, err)
									continue
								}
								Logger.Debugf("DEBUG: Merged block to inject:\n%s", string(mergedBlock))
								finalBlock = string(mergedBlock)
							} else {
								Logger.Infof("Key '%s' exists but new value is not a map, replacing", key)
							}
						} else {
							Logger.Infof("Key '%s' exists but existing value is not a map, replacing", key)
						}
					}
				} else {
					Logger.Errorf("injectNewValuesIntoRoot: key '%s' exists with scalar value '%s'", key, existingValue)
					Logger.Fatalf("injectNewValuesIntoRoot: failed to unmarshal existing content for key '%s': %v", key, err)
				}
				// Remove the old key and its content
				lines = append(lines[:startLine], lines[endLine+1:]...)
			} else {
				Logger.Infof("injectNewValuesIntoRoot: key '%s' does not exist, adding new", key)
			}

			// Inject the new block at the appropriate location
			// If key existed, inject at the same location; otherwise, append at the end
			insertPos := startLine
			if !keyExists {
				// Need to find the right place to insert - either at the end of the wrapper content
				// or at the very end of the file if no wrapper exists
				if indentOffset > 0 {
					// Wrapper pattern exists - find the last line that belongs to the wrapper
					insertPos = findWrapperEndPosition(lines, indentOffset)
				} else {
					// No wrapper - append at the end
					insertPos = len(lines)
				}

				// Add a blank line before the new section if file doesn't end with blank line
				if insertPos > 0 && insertPos <= len(lines) && strings.TrimSpace(lines[insertPos-1]) != "" {
					lines = append(lines[:insertPos], append([]string{""}, lines[insertPos:]...)...)
					insertPos++
				}
			}

			// Prepare the block lines to inject (use finalBlock which contains merged content)
			Logger.Debugf("DEBUG: Finalblock: %+v", finalBlock)
			//fmt.Scanln()
			//fmt.Println("custom_values_mod.go: Preparing to inject finalBlock..")

			blockLines := strings.Split(strings.TrimSpace(finalBlock), "\n")
			Logger.Debugf("DEBUG: Preparing to inject %d lines for key '%s'", len(blockLines), key)
			Logger.Debugf("DEBUG: First 5 lines of finalBlock:\n%s", strings.Join(blockLines[:min(5, len(blockLines))], "\n"))
			var newLines []string

			// Find the base indentation of the first line (usually 0 for root keys)
			baseIndent := 0
			if len(blockLines) > 0 {
				baseIndent = getIndentation(blockLines[0])
			}

			for _, blockLine := range blockLines {
				trimmedLine := strings.TrimSpace(blockLine)
				if trimmedLine == "" {
					continue
				}
				// Calculate the relative indentation from the base
				lineIndent := getIndentation(blockLine)
				relativeIndent := lineIndent - baseIndent
				// Apply indentOffset + relative indentation
				newLines = append(newLines, strings.Repeat(" ", indentOffset+relativeIndent)+trimmedLine)
			}

			// Insert the new lines at the appropriate position
			Logger.Debugf("DEBUG: Actually inserting %d new lines at position %d (total lines before: %d)", len(newLines), insertPos, len(lines))
			lines = append(lines[:insertPos], append(newLines, lines[insertPos:]...)...)
			Logger.Debugf("DEBUG: Total lines after insertion: %d", len(lines))

			modified = true
			if keyExists {
				Logger.Infof("Overwrote root-level key '%s' in values.yaml", key)
			} else {
				Logger.Infof("Injected new root-level key '%s' into values.yaml", key)
			}
		}
	}

	return strings.Join(lines, "\n"), modified
}

// findAndRemoveRootKey checks if a key exists at the root level (accounting for wrapper patterns)
// Returns: (keyExists, valuePreview, startLine, endLine)
// startLine is the line where the key is defined
// endLine is the last line of content belonging to this key (inclusive)
func findAndRemoveRootKey(lines []string, targetKey string, indentOffset int) (bool, string, int, int) {
	pathStack := NewPathStack()
	var keyStartLine, keyEndLine int
	keyFound := false
	keyVirtualIndent := -1
	valuePreview := ""

	for i, line := range lines {
		yl := ParseLine(line)

		// Skip empty lines and comments while searching, but include them in the range if we found the key
		if yl.IsEmpty || yl.IsComment {
			if keyFound {
				// This empty/comment line might belong to our key's content
				keyEndLine = i
			}
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

		// If we've found the key and this line is at the same or lower virtual indent, we're done
		if keyFound && indent <= keyVirtualIndent {
			keyEndLine = i - 1
			// Trim trailing empty lines and comments from the range
			for keyEndLine > keyStartLine && (strings.TrimSpace(lines[keyEndLine]) == "" || strings.HasPrefix(strings.TrimSpace(lines[keyEndLine]), "#")) {
				keyEndLine--
			}
			break
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
				// app: (targetKey, we are here)
				//   key: value
				keyFound = true
				keyStartLine = i
				keyVirtualIndent = indent
				valuePreview = yl.Value
				keyEndLine = i // Default to same line if no content follows
			} else if keyFound {
				// This is content belonging to our key
				// app:
				//   key: value (we are here)
				keyEndLine = i
			}
		} else if keyFound {
			// Non-key line (like list items) that belongs to our key
			keyEndLine = i
		}
	}

	// If key was found and we reached the end of file, include everything up to the last content line
	if keyFound && keyEndLine < keyStartLine {
		// This should never be executed, but just in case if it does we need to log it
		Logger.Errorf("Unexpected condition: keyEndLine < keyStartLine for key '%s'", targetKey)
		keyEndLine = len(lines) - 1
		// Trim trailing empty lines
		for keyEndLine > keyStartLine && strings.TrimSpace(lines[keyEndLine]) == "" {
			keyEndLine--
		}
	}

	return keyFound, valuePreview, keyStartLine, keyEndLine
}
