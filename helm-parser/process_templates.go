package helm_parser

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// ProcessTemplates goes through the chart directory and if the template file contains a kubernetes resource kind like Deployment, StatefulSet, DaemonSet, Job, CronJob,
// adds inline injector specs to both the pod and container levels. It reads the template file, parses it as text,
// and locates where the pod spec and container specs are defined, then adds the appropriate inline injector blocks.
// If templates reference .Values, it injects into values.yaml instead of directly into templates.
func ProcessTemplates(chartDir string, values map[any]any, customYaml string, criticalDs bool, controlPlane bool) error {
	// Load blocks once for all templates
	blocks, err := loadInjectorBlocks(customYaml)
	if err != nil {
		return fmt.Errorf("failed to load injector blocks: %v", err)
	}

	// Track which .Values paths are referenced across all templates
	var allValueReferences []ValueReference
	seenPaths := make(map[string]bool)

	templatesPath := filepath.Join(chartDir, "templates")
	if !CheckHelmTemplateDir(templatesPath) {
		return fmt.Errorf("unable to read from templates directory %s", templatesPath)
	}

	// First pass: detect all .Values references
	err = filepath.Walk(templatesPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip files we can't read
		}

		// Detect value references in this template
		refs := DetectValueReferences(string(content))
		for _, ref := range refs {
			pathKey := strings.Join(ref.Path, ".")
			if !seenPaths[pathKey] {
				seenPaths[pathKey] = true
				allValueReferences = append(allValueReferences, ref)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// if templates reference .Values, and if we find our custom injector block keys in values.yaml,
	// inject custom values into values.yaml instead of directly into templates
	if len(allValueReferences) > 0 {
		//Logger.Infof("Detected .Values references: %v", formatValueReferences(allValueReferences))
		if err := InjectIntoValuesFile(chartDir, blocks, allValueReferences, criticalDs, controlPlane); err != nil {
			Logger.Warnf("Failed to inject into values.yaml: %v", err)
		}
	}

	// Second pass: process templates (inject directly only if not using .Values)
	err = filepath.Walk(templatesPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Read the template file
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read template file %s: %v", path, err)
		}

		// Check if the file contains a Kubernetes resource kind that needs injection
		if kind := getK8sResourceKind(string(content)); kind != "" {
			// Detect which values this template references
			valueRefs := DetectValueReferences(string(content))

			modifiedContent := string(content)
			modified := false

			// Inject pod-level blocks - only inject keys that don't use .Values
			if len(blocks["allPods"]) > 0 || (criticalDs && len(blocks["criticalDsPods"]) > 0) || (controlPlane && len(blocks["controlPlanePods"]) > 0) {
				// Combine pod blocks based on flags
				combinedPodBlocks := blocks["allPods"]
				if criticalDs {
					combinedPodBlocks = append(combinedPodBlocks, blocks["criticalDsPods"]...)
				}
				if controlPlane {
					combinedPodBlocks = append(combinedPodBlocks, blocks["controlPlanePods"]...)
				}

				// Extract pod-level keys dynamically from blocks
				podKeys := extractContainerBlockKeys(combinedPodBlocks) // reuse same function

				// Build a map of which keys use .Values
				keysUsingValues := make(map[string]bool)
				for _, ref := range valueRefs {
					for _, podKey := range podKeys {
						if ref.Key == podKey {
							keysUsingValues[podKey] = true
						}
					}
				}

				// Filter blocks to only include keys that don't use .Values
				blocksToInject := []string{}
				keysToInject := []string{}
				for _, block := range combinedPodBlocks {
					var blockData map[string]interface{}
					if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
						continue
					}
					for key := range blockData {
						if !keysUsingValues[key] {
							blocksToInject = append(blocksToInject, block)
							keysToInject = append(keysToInject, key)
							break
						}
					}
				}

				if len(blocksToInject) > 0 {
					// Inject only the blocks that don't use .Values
					if !modified {
						Logger.Infof("Processing template file for inline injector: %s", path)
					}
					tempBlocks := map[string][]string{"allPods": blocksToInject}
					modifiedContent, err = injectInlinePodSpec(modifiedContent, tempBlocks, kind, criticalDs, controlPlane)
					if err != nil {
						return fmt.Errorf("failed to inject inline pod spec in file %s: %v", path, err)
					}
					modified = true
					Logger.Infof("Injected pod keys %v inline (not using .Values)", keysToInject)
				}

				if len(keysUsingValues) > 0 {
					Logger.Infof("Skipping inline injection for pod keys using .Values: %v", getKeysFromMap(keysUsingValues))
				}
			}

			// Inject container-level blocks - only inject keys that don't use .Values
			if len(blocks["allContainers"]) > 0 {
				// Extract container-level keys dynamically from blocks
				containerKeys := extractContainerBlockKeys(blocks["allContainers"])

				// Build a map of which keys use .Values
				keysUsingValues := make(map[string]bool)
				for _, ref := range valueRefs {
					for _, containerKey := range containerKeys {
						if ref.Key == containerKey {
							keysUsingValues[containerKey] = true
						}
					}
				}

				// Filter blocks to only include keys that don't use .Values
				blocksToInject := []string{}
				keysToInject := []string{}
				for _, block := range blocks["allContainers"] {
					var blockData map[string]interface{}
					if err := yaml.Unmarshal([]byte(block), &blockData); err != nil {
						continue
					}
					for key := range blockData {
						if !keysUsingValues[key] {
							blocksToInject = append(blocksToInject, block)
							keysToInject = append(keysToInject, key)
							break
						}
					}
				}

				if len(blocksToInject) > 0 {
					// Inject only the blocks that don't use .Values
					if !modified {
						Logger.Infof("Processing template file for inline injector: %s", path)
					}
					tempBlocks := map[string][]string{"allContainers": blocksToInject}
					modifiedContent, err = injectInlineContainerSpecWithBlocks(modifiedContent, tempBlocks)
					if err != nil {
						return fmt.Errorf("failed to inject inline container spec in file %s: %v", path, err)
					}
					modified = true
					Logger.Infof("Injected container keys %v inline (not using .Values)", keysToInject)
				}

				if len(keysUsingValues) > 0 {
					Logger.Infof("Skipping inline injection for keys using .Values: %v", getKeysFromMap(keysUsingValues))
				}
			} // Write back the modified content if we made changes
			if modified {
				if err := os.WriteFile(path, []byte(modifiedContent), info.Mode()); err != nil {
					return fmt.Errorf("failed to write modified template file %s: %v", path, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// formatValueReferences formats ValueReference slice for logging
func formatValueReferences(refs []ValueReference) []string {
	result := make([]string, len(refs))
	for i, ref := range refs {
		result[i] = strings.Join(ref.Path, ".")
	}
	return result
}

// getKeysFromMap extracts keys from a map[string]bool
func getKeysFromMap(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func getK8sResourceKind(s string) string {
	// Check for Kubernetes resource kinds that have pod specs
	// Use word boundaries to ensure exact matches (e.g., "Pod" but not "PodDisruptionBudget")
	resourceKinds := []string{
		"Deployment",
		"StatefulSet",
		"DaemonSet",
		"Job",
		"CronJob",
		"ReplicaSet",
		"Pod",
	}

	lines := strings.Split(s, "\n")
	var kindValue string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check if line starts with "kind:" followed by one of our resource kinds
		if strings.HasPrefix(trimmed, "kind:") {
			// Extract the kind value (everything after "kind:")
			kindValueRaw := strings.TrimSpace(strings.TrimPrefix(trimmed, "kind:"))
			if strings.Contains(kindValueRaw, "{") {
				// We hit a Helm template expression
				if strings.Contains(kindValueRaw, "default") {
					kindValueRaw = strings.Split(kindValueRaw, "default")[1]
					kindValue = strings.Trim(kindValueRaw, `"'} `)
				} else {
					// do we need to parse .Values.kind?
					continue
				}
			} else {
				kindValue = kindValueRaw
			}
			// Check for exact match
			Logger.Infof("Found resource kind: %s", kindValue)
			for _, kind := range resourceKinds {
				if kindValue == kind {
					return kind
				}
			}
		}
	}
	return ""
}

// InjectorBlocks stores the injection blocks by category
// Each category (allPods, allContainers, etc.) contains a list of YAML block strings
type InjectorBlocks map[string][]string

func loadInjectorBlocks(customYaml string) (InjectorBlocks, error) {
	// Get the directory of this source file
	// _, filename, _, ok := runtime.Caller(0)
	// if !ok {
	// 	return nil, fmt.Errorf("failed to get current file path")
	// }

	// Get the directory containing this file
	// dir := filepath.Dir(filename)

	// // Construct the path to inject-blocks.yaml relative to this file
	// yamlPath := filepath.Join(dir, customYaml)

	// Read yaml file from disk
	data, err := os.ReadFile(customYaml)
	if err != nil {
		return nil, fmt.Errorf("failed to read inline injector container spec file: %v", err)
	}

	// Parse the YAML structure
	// The structure is: top-level keys -> list of YAML blocks
	var rawBlocks map[string][]interface{}
	if err := yaml.Unmarshal(data, &rawBlocks); err != nil {
		return nil, fmt.Errorf("failed to parse inject-blocks.yaml: %v", err)
	}

	// Convert each block to a string representation
	blocks := make(InjectorBlocks)
	for category, blockList := range rawBlocks {
		blocks[category] = make([]string, 0, len(blockList))
		for _, block := range blockList {
			// Marshal each block back to YAML string
			blockYAML, err := yaml.Marshal(block)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal block in category %s: %v", category, err)
			}
			blocks[category] = append(blocks[category], string(blockYAML))
		}
	}

	return blocks, nil
}

func CheckHelmTemplateDir(templatePath string) bool {
	if _, err := os.Stat(templatePath); err != nil {
		return false
	}
	return true
}

func GetTemplateFiles(templatePath string) ([]string, error) {
	if !CheckHelmTemplateDir(templatePath) {
		return nil, nil
	}
	var templateFiles []string
	err := filepath.WalkDir(templatePath, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			templateFiles = append(templateFiles, path)
		}
		return nil
	})
	if err != nil {
		Logger.Errorf("error walking template path %s: %v", templatePath, err)
		return nil, err
	}
	return templateFiles, nil
}
