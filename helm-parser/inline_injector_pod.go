package helm_parser

import (
	"strings"

	"gopkg.in/yaml.v2"
)

// injectInlinePodSpec injects pod-level blocks from blocks["allPods"] into pod specs
// For Deployment, StatefulSet, DaemonSet: injects under spec.template.spec
// For Pod: injects directly under spec
func injectInlinePodSpec(content string, blocks InjectorBlocks, resourceKind string, criticalDs bool, controlPlane bool) (string, error) {
	lines := strings.Split(content, "\n")
	var result []string
	i := 0

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check if this is a pod spec definition
		// For Pod: spec: directly under kind
		// For Deployment/DaemonSet/etc: spec: under template:
		var isPodSpec bool
		if resourceKind == "Pod" {
			isPodSpec = strings.HasPrefix(trimmed, "spec:") && !isUnderTemplateSection(lines, i)
		} else {
			isPodSpec = strings.HasPrefix(trimmed, "spec:") && isUnderTemplateSection(lines, i)
		}

		if isPodSpec {
			// Add the spec: line
			result = append(result, line)

			// Find the indentation level of the spec
			indent := getIndentation(line)

			// Check which blocks from allPods are missing
			podBlocks := blocks["allPods"]
			if criticalDs {
				podBlocks = append(podBlocks, blocks["criticalDsPods"]...)
			}
			if controlPlane {
				podBlocks = append(podBlocks, blocks["controlPlanePods"]...)
			}

			missingBlocks := findMissingPodBlocks(lines, i, indent, podBlocks)

			if len(missingBlocks) > 0 {
				// Check if we have tolerations blocks to inject and if tolerations already exists
				tolerationBlocks := getPodBlocksByKey(missingBlocks, "tolerations")
				existingTolerationsIdx := findExistingPodKey(lines, i, indent, "tolerations")

				if len(tolerationBlocks) > 0 && existingTolerationsIdx != -1 {
					// Tolerations exist, append our items to it
					insertionPoint := findTolerationsEndPoint(lines, existingTolerationsIdx, indent)

					// Copy lines up to the insertion point
					for j := i + 1; j < insertionPoint; j++ {
						result = append(result, lines[j])
					}

					// Append our toleration items
					// List items start at indent+2, properties at indent+4
					listIndent := strings.Repeat(" ", indent+2)
					for _, block := range tolerationBlocks {
						blockLines := strings.Split(strings.TrimSpace(block), "\n")
						// Skip the "tolerations:" line (index 0) and add the rest
						for idx := 1; idx < len(blockLines); idx++ {
							line := blockLines[idx]
							// If line starts with "- ", it's a list item at indent+2
							// Otherwise it's a property that needs indent+4
							if strings.HasPrefix(strings.TrimSpace(line), "- ") {
								result = append(result, listIndent+strings.TrimSpace(line))
							} else {
								// Property line - preserve its relative indentation from the block
								result = append(result, listIndent+"  "+strings.TrimSpace(line))
							}
						}
					} // Remove tolerations blocks from missing list
					missingBlocks = removePodBlocksByKey(missingBlocks, "tolerations")

					// Now handle remaining blocks (like affinity)
					if len(missingBlocks) > 0 {
						// Find where to inject remaining blocks (after tolerations section)
						injectionPoint := findPodBlockInjectionPoint(lines, insertionPoint-1, indent)
						// Copy from insertion point to injection point
						for j := insertionPoint; j < injectionPoint; j++ {
							result = append(result, lines[j])
						}
						// Inject remaining blocks (like affinity)
						injectedLines := injectMissingPodBlocks(missingBlocks, indent+2)
						result = append(result, injectedLines...)
						i = injectionPoint
					} else {
						i = insertionPoint
					}
				} else {
					// No existing tolerations or no toleration blocks to inject
					injectionPoint := findPodBlockInjectionPoint(lines, i, indent)

					// Add lines from after spec: up to injection point
					for j := i + 1; j < injectionPoint; j++ {
						result = append(result, lines[j])
					}

					// Inject the missing blocks
					result = append(result, injectMissingPodBlocks(missingBlocks, indent+2)...)

					// Continue from injection point
					i = injectionPoint
				}
			} else {
				// All blocks already exist, just copy lines until next major section
				j := i + 1
				for j < len(lines) {
					nextLine := lines[j]
					nextIndent := getIndentation(nextLine)
					nextTrimmed := strings.TrimSpace(nextLine)

					// Stop when we hit another top-level section at same or lower indent
					if nextIndent <= indent && nextTrimmed != "" && !strings.HasPrefix(nextTrimmed, "#") {
						break
					}
					result = append(result, nextLine)
					j++
				}
				i = j
			}
		} else {
			// Not a pod spec definition, just add the line
			result = append(result, line)
			i++
		}
	}

	return strings.Join(result, "\n"), nil
}

// isUnderTemplateSection checks if a spec: line is under a template: section
func isUnderTemplateSection(lines []string, index int) bool {
	lineIndent := getIndentation(lines[index])

	// Look backwards to find the parent section
	for i := index - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		indent := getIndentation(lines[i])

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// If we find a line at lower indentation, it's a parent section
		if indent < lineIndent {
			// Check if the parent is template:
			if strings.HasPrefix(trimmed, "template:") {
				return true
			}
			// If it's another section, not under template
			return false
		}
	}

	return false
}

// findMissingPodBlocks checks which blocks from the provided list are not already present in the pod spec
func findMissingPodBlocks(lines []string, specIndex, specIndent int, blockStrings []string) []string {
	var missing []string

	for _, block := range blockStrings {
		if !podSpecHasBlock(lines, specIndex, specIndent, block) {
			missing = append(missing, block)
		}
	}

	return missing
}

// podSpecHasBlock checks if the pod spec already has the specified block
func podSpecHasBlock(lines []string, specIndex, specIndent int, block string) bool {
	// Parse the block to get its structure
	var blockData map[string]interface{}
	if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
		return false
	}

	// Get the top-level key of the block (tolerations, affinity, etc.)
	var topKey string
	for key := range blockData {
		topKey = key
		break
	}

	if topKey == "" {
		return false
	}

	// For tolerations, check if the specific toleration exists
	if topKey == "tolerations" {
		return podSpecHasTolerationBlock(lines, specIndex, specIndent, blockData)
	}

	// For affinity, check if affinity block exists (we don't want to override existing affinity)
	if topKey == "affinity" {
		return podSpecHasKey(lines, specIndex, specIndent, "affinity")
	}

	// For other block types, check if the key exists
	return podSpecHasKey(lines, specIndex, specIndent, topKey)
}

// podSpecHasTolerationBlock checks if the pod spec has the specific toleration
func podSpecHasTolerationBlock(lines []string, specIndex, specIndent int, blockData map[string]interface{}) bool {
	// Extract toleration details from the block
	tolerationKey := ""
	tolerationOperator := ""
	tolerationEffect := ""

	if tolerationsList, ok := blockData["tolerations"].([]interface{}); ok && len(tolerationsList) > 0 {
		if toleration, ok := tolerationsList[0].(map[interface{}]interface{}); ok {
			if key, ok := toleration["key"].(string); ok {
				tolerationKey = key
			}
			if operator, ok := toleration["operator"].(string); ok {
				tolerationOperator = operator
			}
			if effect, ok := toleration["effect"].(string); ok {
				tolerationEffect = effect
			}
		}
	}

	// Check if this toleration exists in the pod spec's tolerations
	for i := specIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if indent <= specIndent {
			break
		}

		if indent == specIndent+2 && strings.HasPrefix(trimmed, "tolerations:") {
			// Found tolerations block, check for our specific toleration
			// Look for matching key, operator, and effect
			foundKey := false
			foundOperator := false
			foundEffect := false

			for j := i + 1; j < len(lines); j++ {
				nextLine := lines[j]
				nextTrimmed := strings.TrimSpace(nextLine)
				nextIndent := getIndentation(nextLine)

				if nextTrimmed == "" || strings.HasPrefix(nextTrimmed, "#") {
					continue
				}

				if nextIndent < specIndent+2 {
					break
				}

				// Check for matching fields
				if tolerationKey != "" && nextTrimmed == "key: "+tolerationKey {
					foundKey = true
				}
				if tolerationOperator != "" && nextTrimmed == "operator: "+tolerationOperator {
					foundOperator = true
				}
				if tolerationEffect != "" && nextTrimmed == "effect: "+tolerationEffect {
					foundEffect = true
				}

				// Reset when we hit a new list item
				if strings.HasPrefix(nextTrimmed, "- ") && j > i+1 {
					// If we found a match in the previous item, return true
					if (tolerationKey == "" || foundKey) &&
						(tolerationOperator == "" || foundOperator) &&
						(tolerationEffect == "" || foundEffect) {
						return true
					}
					// Reset for next item
					foundKey = false
					foundOperator = false
					foundEffect = false
				}
			}

			// Check last item
			if (tolerationKey == "" || foundKey) &&
				(tolerationOperator == "" || foundOperator) &&
				(tolerationEffect == "" || foundEffect) {
				return true
			}

			break
		}
	}

	return false
}

// podSpecHasKey checks if the pod spec has a specific top-level key
func podSpecHasKey(lines []string, specIndex, specIndent int, key string) bool {
	inHelmConditional := false

	for i := specIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Track helm template conditionals - if key is inside {{- with }}, it's conditional
		if strings.HasPrefix(trimmed, "{{-") && strings.Contains(trimmed, "with") {
			inHelmConditional = true
			continue
		}
		if strings.HasPrefix(trimmed, "{{-") && strings.Contains(trimmed, "end") {
			inHelmConditional = false
			continue
		}

		if indent <= specIndent {
			break
		}

		if indent == specIndent+2 && strings.HasPrefix(trimmed, key+":") {
			// If it's inside a helm conditional, treat it as not existing (it's conditional)
			if inHelmConditional {
				return false
			}
			return true
		}
	}
	return false
}

// findPodBlockInjectionPoint finds where to inject blocks in the pod spec
func findPodBlockInjectionPoint(lines []string, specIndex, specIndent int) int {
	// Inject before containers:, initContainers:, or volumes:
	for i := specIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := getIndentation(line)

		if indent <= specIndent {
			return i
		}

		// Inject before containers, initContainers, or volumes
		if indent == specIndent+2 && (strings.HasPrefix(trimmed, "containers:") ||
			strings.HasPrefix(trimmed, "initContainers:") ||
			strings.HasPrefix(trimmed, "volumes:")) {
			return i
		}
	}

	return len(lines)
}

// getPodBlocksByKey returns blocks that have the specified top-level key
func getPodBlocksByKey(blocks []string, key string) []string {
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
	//DEBUG
	//Logger.Infof("getPodBlocksByKey result: %v", result)
	return result
}

// removePodBlocksByKey returns blocks that don't have the specified top-level key
func removePodBlocksByKey(blocks []string, key string) []string {
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

// findExistingPodKey finds the line index of a top-level key in the pod spec, or -1 if not found
func findExistingPodKey(lines []string, specIndex, specIndent int, key string) int {
	for i := specIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if indent <= specIndent {
			break
		}

		if indent == specIndent+2 && strings.HasPrefix(trimmed, key+":") {
			return i
		}
	}
	return -1
}

// findTolerationsEndPoint finds where the tolerations block ends (where to append new items)
func findTolerationsEndPoint(lines []string, tolerationsLineIdx, specIndent int) int {
	for i := tolerationsLineIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := getIndentation(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check for helm template end directives - insert before them
		if strings.HasPrefix(trimmed, "{{-") && strings.Contains(trimmed, "end") {
			return i
		}

		// If we hit a property at the spec level or lower, insert before it
		if indent <= specIndent+2 {
			return i
		}
	}
	return len(lines)
}

// injectMissingPodBlocks injects the missing pod blocks with proper indentation and grouping
func injectMissingPodBlocks(missingBlocks []string, indent int) []string {
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

	// Inject tolerations blocks first (merged under single header)
	if tolerationBlocks, ok := grouped["tolerations"]; ok && len(tolerationBlocks) > 0 {
		result = append(result, spaces+"tolerations:")
		for _, block := range tolerationBlocks {
			blockLines := strings.Split(strings.TrimSpace(block), "\n")
			// Skip the "tolerations:" line and add the rest
			for idx := 1; idx < len(blockLines); idx++ {
				result = append(result, spaces+blockLines[idx])
			}
		}
	}

	// Inject other blocks (affinity, etc.)
	for key, blocks := range grouped {
		if key == "tolerations" {
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
