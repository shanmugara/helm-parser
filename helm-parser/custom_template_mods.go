package helm_parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// FileModification represents a single modification to inject into a file
type FileModification struct {
	Name        string   `yaml:"name"`
	AnchorLines []string `yaml:"anchorLines"`
	Position    string   `yaml:"position"` // "before" or "after"
	Block       string   `yaml:"block"`
	Indent      int      `yaml:"indent"` // Relative indent from anchor (can be negative)
}

// CustomFileMod represents modifications to apply to a specific file
type CustomFileMod struct {
	File          string             `yaml:"file"`
	Modifications []FileModification `yaml:"modifications"`
}

// loadCustomFileMods reads the customFileMods section from inject-blocks.yaml
func loadCustomFileMods(customYaml string) ([]CustomFileMod, error) {
	data, err := os.ReadFile(customYaml)
	if err != nil {
		return nil, fmt.Errorf("failed to read custom file mods file: %v", err)
	}

	// Parse the YAML structure
	var rawConfig map[string]interface{}
	if err := yaml.Unmarshal(data, &rawConfig); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %v", customYaml, err)
	}

	// Extract customFileMods section
	customFileModsRaw, ok := rawConfig["customFileMods"]
	if !ok {
		// No customFileMods section, return empty list
		return []CustomFileMod{}, nil
	}

	// Marshal back to YAML and unmarshal into our struct
	customFileModsYAML, err := yaml.Marshal(customFileModsRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal customFileMods: %v", err)
	}

	var customFileMods []CustomFileMod
	if err := yaml.Unmarshal(customFileModsYAML, &customFileMods); err != nil {
		return nil, fmt.Errorf("failed to unmarshal customFileMods: %v", err)
	}

	return customFileMods, nil
}

// ApplyCustomTemplateMods applies custom file modifications to template files
func ApplyCustomTemplateMods(chartDir string, customYaml string) error {
	customMods, err := loadCustomFileMods(customYaml)
	if err != nil {
		return fmt.Errorf("failed to load custom file mods: %v", err)
	}

	if len(customMods) == 0 {
		return nil
	}

	for _, mod := range customMods {
		filePath := filepath.Join(chartDir, mod.File)

		// Check if file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			Logger.Warnf("File %s does not exist, skipping custom modifications", mod.File)
			continue
		}

		// Read the file
		content, err := os.ReadFile(filePath)
		if err != nil {
			Logger.Errorf("Failed to read file %s: %v", filePath, err)
			continue
		}

		fileContent := string(content)
		modified := false

		// Apply each modification
		for _, modification := range mod.Modifications {
			newContent, changed := applyFileModification(fileContent, modification)
			if changed {
				fileContent = newContent
				modified = true
				Logger.Infof("Applied modification '%s' to %s", modification.Name, mod.File)
			}
		}

		// Write back if modified
		if modified {
			if err := os.WriteFile(filePath, []byte(fileContent), 0644); err != nil {
				return fmt.Errorf("failed to write modified file %s: %v", filePath, err)
			}
			Logger.Infof("Updated file %s with custom modifications", mod.File)
		}
	}

	return nil
}

// applyFileModification applies a single modification to file content
func applyFileModification(content string, mod FileModification) (string, bool) {
	lines := strings.Split(content, "\n")

	// Find the anchor lines
	anchorStartIndex, anchorEndIndex := findAnchorLinesWithRange(lines, mod.AnchorLines)
	if anchorStartIndex == -1 {
		Logger.Warnf("Could not find anchor lines for modification '%s'", mod.Name)
		return content, false
	}

	// Determine insertion point
	var insertIndex int
	if mod.Position == "before" {
		insertIndex = anchorStartIndex
	} else { // "after"
		// Insert after the last anchor line
		insertIndex = anchorEndIndex + 1
	}

	// Check if the block already exists at the insertion point (context-aware check)
	blockLines := strings.Split(strings.TrimSpace(mod.Block), "\n")
	if blockAlreadyExistsAtPosition(lines, blockLines, insertIndex) {
		Logger.Infof("Modification '%s' already exists at position, skipping", mod.Name)
		return content, false
	}

	// Get the indentation from the first anchor line
	baseIndent := getIndentation(lines[anchorStartIndex])

	// Apply relative indent if specified
	finalIndent := baseIndent + mod.Indent
	if finalIndent < 0 {
		finalIndent = 0
	}

	// Prepare the block to insert with proper indentation
	blockToInsert := prepareBlockForInsertion(mod.Block, finalIndent)

	// Insert the block
	result := make([]string, 0, len(lines)+len(blockToInsert))
	result = append(result, lines[:insertIndex]...)
	result = append(result, blockToInsert...)
	result = append(result, lines[insertIndex:]...)

	return strings.Join(result, "\n"), true
}

// findAnchorLinesWithRange finds the start and end indices of anchor lines
// Returns (-1, -1) if not found
// The anchor lines must appear in order but can have other lines in between
func findAnchorLinesWithRange(lines []string, anchorLines []string) (int, int) {
	if len(anchorLines) == 0 {
		return -1, -1
	}

	// Search for the sequence of anchor lines (allowing lines in between)
	for i := 0; i < len(lines); i++ {
		// Try to match starting from this line
		anchorIdx := 0
		firstMatchIdx := -1
		lastMatchIdx := -1

		for j := i; j < len(lines) && anchorIdx < len(anchorLines); j++ {
			lineTrimmed := strings.TrimSpace(lines[j])
			anchorTrimmed := strings.TrimSpace(anchorLines[anchorIdx])

			// Debug logging
			// Logger.Debugf("Comparing line %d '%s' with anchor[%d] '%s'", j, lineTrimmed, anchorIdx, anchorTrimmed)

			// Trim and compare (allows for indentation differences)
			if lineTrimmed == anchorTrimmed {
				if firstMatchIdx == -1 {
					firstMatchIdx = j
				}
				lastMatchIdx = j
				anchorIdx++
				// Logger.Debugf("Match! anchorIdx now %d", anchorIdx)
			}
		}

		// If we matched all anchor lines, return the range
		if anchorIdx == len(anchorLines) {
			Logger.Debugf("Found all %d anchor lines from line %d to %d", len(anchorLines), firstMatchIdx, lastMatchIdx)
			return firstMatchIdx, lastMatchIdx
		}
	}

	Logger.Warnf("Could not find anchor sequence. Looking for: %v", anchorLines)
	return -1, -1
}

// blockAlreadyExistsAtPosition checks if a block already exists at a specific position in the file
// This is a context-aware check that looks for the block content starting at insertIndex
func blockAlreadyExistsAtPosition(fileLines []string, blockLines []string, insertIndex int) bool {
	if len(blockLines) == 0 {
		return false
	}

	// Create a normalized version of the block for comparison
	normalizedBlock := make([]string, 0, len(blockLines))
	for _, line := range blockLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			normalizedBlock = append(normalizedBlock, trimmed)
		}
	}

	if len(normalizedBlock) == 0 {
		return false
	}

	// Check if we have enough lines to compare
	if insertIndex+len(normalizedBlock) > len(fileLines) {
		return false
	}

	// Compare the block with the content at the insertion position
	for i, blockLine := range normalizedBlock {
		fileLine := strings.TrimSpace(fileLines[insertIndex+i])
		if fileLine != blockLine {
			return false
		}
	}

	return true
}

// prepareBlockForInsertion prepares a block for insertion with proper indentation
func prepareBlockForInsertion(block string, baseIndent int) []string {
	blockLines := strings.Split(strings.TrimSpace(block), "\n")
	result := make([]string, 0, len(blockLines))

	// Find the minimum indentation in the block
	minIndent := -1
	for _, line := range blockLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		indent := getIndentation(line)
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent == -1 {
		minIndent = 0
	}

	// Apply base indentation while preserving relative indentation
	for _, line := range blockLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			result = append(result, "")
			continue
		}

		lineIndent := getIndentation(line)
		relativeIndent := lineIndent - minIndent
		newIndent := baseIndent + relativeIndent

		result = append(result, strings.Repeat(" ", newIndent)+trimmed)
	}

	return result
}
