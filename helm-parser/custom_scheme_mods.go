package helm_parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

type SchemaModBlocks struct {
	FileName      string      `yaml:"file"`
	Modifications []SchemaMod `yaml:"modifications"`
}

type SchemaMod struct {
	Name  string                 `yaml:"name"`
	Root  map[string]interface{} `yaml:"root"`
	Block string                 `yaml:"block"`
}

func ApplyCustomSchemaMods(chartDir string, customYaml string) error {
	Logger.Infof("Applying custom schema modifications from %s", customYaml)
	data, err := os.ReadFile(customYaml)
	if err != nil {
		return fmt.Errorf("failed to read custom scheme mods file: %v", err)
	}

	// Parse the YAML structure
	var rawConfig map[string]interface{}
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return fmt.Errorf("failed to parse %s: %v", customYaml, err)
	}

	// Extract customSchemaMods section
	customSchemaModsRaw, ok := rawConfig["customSchemaMods"]
	if !ok {
		// No customSchemaMods section, return empty map
		Logger.Infof("No custom schema modifications found")
		return nil
	}
	Logger.Infof("Found custom schema modifications")

	// Marshal back to YAML and unmarshal into our struct
	customSchemaModsYAML, err := yaml.Marshal(customSchemaModsRaw)
	if err != nil {
		return fmt.Errorf("failed to marshal customSchemaMods: %v", err)
	}
	Logger.Infof("Marshalled custom schema modifications YAML")

	var customSchemaModsList []SchemaModBlocks
	if err := yaml.Unmarshal(customSchemaModsYAML, &customSchemaModsList); err != nil {
		return fmt.Errorf("failed to unmarshal customSchemaMods: %v", err)
	}

	// Apply modifications for each file
	for _, customSchemaMods := range customSchemaModsList {
		err = updateSchemaFile(chartDir, customSchemaMods)
		if err != nil {
			return fmt.Errorf("failed to update schema file %s: %v", customSchemaMods.FileName, err)
		}
	}

	return nil
}

func updateSchemaFile(chartDir string, mods SchemaModBlocks) error {
	// Read existing json schema file
	schemaFile := filepath.Join(chartDir, mods.FileName)
	data, err := os.ReadFile(schemaFile)
	if err != nil {
		return fmt.Errorf("failed to read schema file: %v", err)
	}
	// Parse existing schema into map
	jsonSchema := map[string]interface{}{}
	if err := json.Unmarshal(data, &jsonSchema); err != nil {
		return fmt.Errorf("failed to parse schema file: %v", err)
	}

	// Apply modifications
	for _, mod := range mods.Modifications {
		// Parse the block string into a map
		var blockMap interface{}
		if err := yaml.Unmarshal([]byte(mod.Block), &blockMap); err != nil {
			return fmt.Errorf("failed to parse block for modification '%s': %v", mod.Name, err)
		}

		// Convert to JSON-compatible format (map[string]interface{})
		blockMapConverted, err := convertToStringMap(blockMap)
		if err != nil {
			return fmt.Errorf("failed to convert block for modification '%s': %v", mod.Name, err)
		}

		blockMapTyped, ok := blockMapConverted.(map[string]interface{})
		if !ok {
			return fmt.Errorf("block for modification '%s' is not a map", mod.Name)
		}

		// Determine the target map where we'll inject the block
		targetMap := jsonSchema
		if len(mod.Root) > 0 {
			// Traverse the path specified in Root
			path := extractPath(mod.Root)
			Logger.Infof("Traversing path: %v", path)

			var err error
			targetMap, err = traversePath(jsonSchema, path)
			if err != nil {
				return fmt.Errorf("failed to traverse path for modification '%s': %v", mod.Name, err)
			}
		}

		// Apply the block to the target location
		Logger.Infof("Applying schema modification: %s", mod.Name)
		for k, v := range blockMapTyped {
			Logger.Infof(" - Setting key: %s", k)
			Logger.Infof("   Value: %v", v)
			targetMap[k] = v
		}
	}

	// Marshal back to JSON with indentation
	updatedData, err := json.MarshalIndent(jsonSchema, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal updated schema: %v", err)
	}

	// Write back to file (using full path)
	if err := os.WriteFile(schemaFile, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write updated schema file: %v", err)
	}

	return nil
}

// extractPath converts the nested map structure from Root into a slice of keys representing the path
// Example: {"$schema": {"$defs": "properties"}} becomes ["$schema", "$defs", "properties"]
func extractPath(root map[string]interface{}) []string {
	var path []string
	current := root

	for len(current) > 0 {
		// There should only be one key at each level
		for key, value := range current {
			path = append(path, key)

			// If value is a map, continue traversing
			if nestedMap, ok := value.(map[interface{}]interface{}); ok {
				// Convert map[interface{}]interface{} to map[string]interface{}
				current = make(map[string]interface{})
				for k, v := range nestedMap {
					if strKey, ok := k.(string); ok {
						current[strKey] = v
					}
				}
			} else if nestedMap, ok := value.(map[string]interface{}); ok {
				current = nestedMap
			} else if strValue, ok := value.(string); ok {
				// If it's a string, it's the last element in the path
				path = append(path, strValue)
				return path
			} else {
				// End of path
				return path
			}
			break // Only process the first (and should be only) key
		}
	}

	return path
}

// traversePath navigates through the nested map structure following the given path
// and returns the target map where modifications should be applied
func traversePath(schema map[string]interface{}, path []string) (map[string]interface{}, error) {
	current := schema

	for i, key := range path {
		// If this is the last key in the path, return the current map
		// The modification will be applied to this map
		if i == len(path)-1 {
			// Ensure the key exists and is a map
			if _, exists := current[key]; !exists {
				// Create the map if it doesn't exist
				current[key] = make(map[string]interface{})
			}

			targetMap, ok := current[key].(map[string]interface{})
			if !ok {
				// Try to convert from map[interface{}]interface{}
				if interfaceMap, ok := current[key].(map[interface{}]interface{}); ok {
					targetMap = make(map[string]interface{})
					for k, v := range interfaceMap {
						if strKey, ok := k.(string); ok {
							targetMap[strKey] = v
						}
					}
					current[key] = targetMap
				} else {
					return nil, fmt.Errorf("key '%s' is not a map", key)
				}
			}
			return targetMap, nil
		}

		// Navigate to the next level
		nextLevel, exists := current[key]
		if !exists {
			return nil, fmt.Errorf("key '%s' not found in schema", key)
		}

		nextMap, ok := nextLevel.(map[string]interface{})
		if !ok {
			// Try to convert from map[interface{}]interface{}
			if interfaceMap, ok := nextLevel.(map[interface{}]interface{}); ok {
				nextMap = make(map[string]interface{})
				for k, v := range interfaceMap {
					if strKey, ok := k.(string); ok {
						nextMap[strKey] = v
					}
				}
				current[key] = nextMap
			} else {
				return nil, fmt.Errorf("key '%s' is not a map, cannot traverse further", key)
			}
		}

		current = nextMap
	}

	return current, nil
}

// convertToStringMap recursively converts map[interface{}]interface{} to map[string]interface{}
// to make it compatible with JSON marshaling
func convertToStringMap(i interface{}) (interface{}, error) {
	switch x := i.(type) {
	case map[interface{}]interface{}:
		m := make(map[string]interface{})
		for k, v := range x {
			strKey, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("non-string key found: %v", k)
			}
			converted, err := convertToStringMap(v)
			if err != nil {
				return nil, err
			}
			m[strKey] = converted
		}
		return m, nil
	case map[string]interface{}:
		m := make(map[string]interface{})
		for k, v := range x {
			converted, err := convertToStringMap(v)
			if err != nil {
				return nil, err
			}
			m[k] = converted
		}
		return m, nil
	case []interface{}:
		arr := make([]interface{}, len(x))
		for i, v := range x {
			converted, err := convertToStringMap(v)
			if err != nil {
				return nil, err
			}
			arr[i] = converted
		}
		return arr, nil
	default:
		return i, nil
	}
}
