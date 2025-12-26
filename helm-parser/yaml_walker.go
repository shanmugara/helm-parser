package helm_parser

import (
	"strings"
	"unicode"
)

// YAMLLine represents a parsed line in a YAML file
type YAMLLine struct {
	Raw         string // Original line
	Trimmed     string // Trimmed content
	Indent      int    // Indentation level (spaces)
	IsEmpty     bool   // Is empty or whitespace only
	IsComment   bool   // Is a comment line
	Key         string // Key if this is a key:value line
	Value       string // Value if this is a key:value line
	HasColon    bool   // Whether line contains ":"
	ValueIndent int    // Number of spaces before value (for alignment)
}

// ParseLine parses a YAML line into a YAMLLine struct
func ParseLine(line string) YAMLLine {
	trimmed := strings.TrimSpace(line)
	indent := GetIndentation(line)

	yl := YAMLLine{
		Raw:       line,
		Trimmed:   trimmed,
		Indent:    indent,
		IsEmpty:   trimmed == "",
		IsComment: strings.HasPrefix(trimmed, "#"),
		HasColon:  strings.Contains(trimmed, ":"),
	}

	// Extract key and value if this is a key:value line
	if yl.HasColon && !yl.IsComment {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			yl.Key = strings.TrimSpace(parts[0])
			yl.Value = strings.TrimSpace(parts[1])
			yl.ValueIndent = len(parts[1]) - len(strings.TrimLeftFunc(parts[1], unicode.IsSpace))
		}
	}

	return yl
}

// GetIndentation returns the number of spaces at the start of a line
func GetIndentation(line string) int {
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

// IsEmptyOrComment checks if a line is empty or a comment
func IsEmptyOrComment(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// FindNextNonEmptyLine finds the next non-empty, non-comment line starting from index
// Returns the index and the line, or -1 if not found
func FindNextNonEmptyLine(lines []string, startIndex int) (int, string) {
	for i := startIndex; i < len(lines); i++ {
		if !IsEmptyOrComment(lines[i]) {
			return i, lines[i]
		}
	}
	return -1, ""
}

// FindLineAtIndent finds the next line at or below the specified indent level
// starting from startIndex. Returns index or -1 if not found.
func FindLineAtIndent(lines []string, startIndex, maxIndent int) int {
	for i := startIndex; i < len(lines); i++ {
		if IsEmptyOrComment(lines[i]) {
			continue
		}
		if GetIndentation(lines[i]) <= maxIndent {
			return i
		}
	}
	return len(lines)
}

// SkipChildLines skips all lines that are children of the current indent level
// Returns the index of the first line at same or lower indent
func SkipChildLines(lines []string, currentIndex, currentIndent int) int {
	for i := currentIndex + 1; i < len(lines); i++ {
		if IsEmptyOrComment(lines[i]) {
			continue
		}
		if GetIndentation(lines[i]) <= currentIndent {
			return i
		}
	}
	return len(lines)
}

// CollectChildLines collects all child lines (higher indent) of the current line
// Returns slice of child lines and the index where children end
func CollectChildLines(lines []string, currentIndex, currentIndent int) ([]string, int) {
	var children []string
	i := currentIndex + 1

	for i < len(lines) {
		line := lines[i]
		if IsEmptyOrComment(line) {
			children = append(children, line)
			i++
			continue
		}

		indent := GetIndentation(line)
		if indent <= currentIndent {
			break
		}

		children = append(children, line)
		i++
	}

	return children, i
}

// IndentLines adds the specified number of spaces to the beginning of each line
func IndentLines(lines []string, spaces int) []string {
	prefix := strings.Repeat(" ", spaces)
	result := make([]string, len(lines))
	for i, line := range lines {
		if IsEmptyOrComment(line) {
			result[i] = line // Don't indent empty lines or comments
		} else {
			result[i] = prefix + line
		}
	}
	return result
}

// ExtractKeyValue extracts key and value from a "key: value" line
func ExtractKeyValue(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, ":") {
		return "", "", false
	}

	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// IsListItem checks if a line is a YAML list item (starts with "- ")
func IsListItem(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "- ")
}

// PathStack represents a stack of YAML path elements for tracking position in hierarchy
type PathStack struct {
	levels []PathLevel
}

// PathLevel represents one level in a YAML path
type PathLevel struct {
	Indent int
	Key    string
}

// NewPathStack creates a new empty path stack
func NewPathStack() *PathStack {
	return &PathStack{levels: make([]PathLevel, 0)}
}

// Push adds a level to the stack
func (ps *PathStack) Push(indent int, key string) {
	ps.levels = append(ps.levels, PathLevel{Indent: indent, Key: key})
}

// Pop removes levels from the stack based on indentation
func (ps *PathStack) PopToIndent(indent int) {
	for len(ps.levels) > 0 && ps.levels[len(ps.levels)-1].Indent >= indent {
		ps.levels = ps.levels[:len(ps.levels)-1]
	}
}

// CurrentPath returns the current path as a slice of keys
func (ps *PathStack) CurrentPath() []string {
	path := make([]string, len(ps.levels))
	for i, level := range ps.levels {
		path[i] = level.Key
	}
	return path
}

// Depth returns the current depth of the path
func (ps *PathStack) Depth() int {
	return len(ps.levels)
}

// Clear clears the entire stack
func (ps *PathStack) Clear() {
	ps.levels = ps.levels[:0]
}
