package diff

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// -------------------------------------------------------------------------
// Core Types
// -------------------------------------------------------------------------

// LineType represents the kind of line in a diff.
type LineType int

const (
	LineContext LineType = iota // Line exists in both files
	LineAdded                   // Line added in the new file
	LineRemoved                 // Line removed from the old file
)

// Segment represents a portion of a line for intra-line highlighting
type Segment struct {
	Start int
	End   int
	Type  LineType
	Text  string
}

// DiffLine represents a single line in a diff
type DiffLine struct {
	OldLineNo int       // Line number in old file (0 for added lines)
	NewLineNo int       // Line number in new file (0 for removed lines)
	Kind      LineType  // Type of line (added, removed, context)
	Content   string    // Content of the line
	Segments  []Segment // Segments for intraline highlighting
}

// Hunk represents a section of changes in a diff
type Hunk struct {
	Header string
	Lines  []DiffLine
}

// DiffResult contains the parsed result of a diff
type DiffResult struct {
	OldFile string
	NewFile string
	Hunks   []Hunk
}

// linePair represents a pair of lines for side-by-side display
type linePair struct {
	left  *DiffLine
	right *DiffLine
}

// -------------------------------------------------------------------------
// Parse Configuration
// -------------------------------------------------------------------------

// ParseConfig configures the behavior of diff parsing
type ParseConfig struct {
	ContextSize int // Number of context lines to include
}

// ParseOption modifies a ParseConfig
type ParseOption func(*ParseConfig)

// WithContextSize sets the number of context lines to include
func WithContextSize(size int) ParseOption {
	return func(p *ParseConfig) {
		if size >= 0 {
			p.ContextSize = size
		}
	}
}

// -------------------------------------------------------------------------
// Side-by-Side Configuration
// -------------------------------------------------------------------------

// SideBySideConfig configures the rendering of side-by-side diffs
type SideBySideConfig struct {
	TotalWidth int
}

// SideBySideOption modifies a SideBySideConfig
type SideBySideOption func(*SideBySideConfig)

// NewSideBySideConfig creates a SideBySideConfig with default values
func NewSideBySideConfig(opts ...SideBySideOption) SideBySideConfig {
	config := SideBySideConfig{
		TotalWidth: 160, // Default width for side-by-side view
	}

	for _, opt := range opts {
		opt(&config)
	}

	return config
}

// WithTotalWidth sets the total width for side-by-side view
func WithTotalWidth(width int) SideBySideOption {
	return func(s *SideBySideConfig) {
		if width > 0 {
			s.TotalWidth = width
		}
	}
}

// -------------------------------------------------------------------------
// Diff Parsing
// -------------------------------------------------------------------------

// ParseUnifiedDiff parses a unified diff format string into structured data
func ParseUnifiedDiff(diff string) (DiffResult, error) {
	var result DiffResult
	var currentHunk *Hunk

	hunkHeaderRe := regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@`)
	lines := strings.Split(diff, "\n")

	var oldLine, newLine int
	inFileHeader := true

	for _, line := range lines {
		// Parse file headers
		if inFileHeader {
			if strings.HasPrefix(line, "--- a/") {
				result.OldFile = strings.TrimPrefix(line, "--- a/")
				continue
			}
			if strings.HasPrefix(line, "+++ b/") {
				result.NewFile = strings.TrimPrefix(line, "+++ b/")
				inFileHeader = false
				continue
			}
		}

		// Parse hunk headers
		if matches := hunkHeaderRe.FindStringSubmatch(line); matches != nil {
			if currentHunk != nil {
				result.Hunks = append(result.Hunks, *currentHunk)
			}
			currentHunk = &Hunk{
				Header: line,
				Lines:  []DiffLine{},
			}

			oldStart, _ := strconv.Atoi(matches[1])
			newStart, _ := strconv.Atoi(matches[3])
			oldLine = oldStart
			newLine = newStart
			continue
		}

		// Ignore "No newline at end of file" markers
		if strings.HasPrefix(line, "\\ No newline at end of file") {
			continue
		}

		if currentHunk == nil {
			continue
		}

		// Process the line based on its prefix
		if len(line) > 0 {
			switch line[0] {
			case '+':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					OldLineNo: 0,
					NewLineNo: newLine,
					Kind:      LineAdded,
					Content:   line[1:],
				})
				newLine++
			case '-':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					OldLineNo: oldLine,
					NewLineNo: 0,
					Kind:      LineRemoved,
					Content:   line[1:],
				})
				oldLine++
			default:
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					OldLineNo: oldLine,
					NewLineNo: newLine,
					Kind:      LineContext,
					Content:   line,
				})
				oldLine++
				newLine++
			}
		} else {
			// Handle empty lines
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				OldLineNo: oldLine,
				NewLineNo: newLine,
				Kind:      LineContext,
				Content:   "",
			})
			oldLine++
			newLine++
		}
	}

	// Add the last hunk if there is one
	if currentHunk != nil {
		result.Hunks = append(result.Hunks, *currentHunk)
	}

	return result, nil
}

// HighlightIntralineChanges updates lines in a hunk to show character-level differences
func HighlightIntralineChanges(h *Hunk) {
	var updated []DiffLine
	dmp := diffmatchpatch.New()

	for i := 0; i < len(h.Lines); i++ {
		// Look for removed line followed by added line
		if i+1 < len(h.Lines) &&
			h.Lines[i].Kind == LineRemoved &&
			h.Lines[i+1].Kind == LineAdded {

			oldLine := h.Lines[i]
			newLine := h.Lines[i+1]

			// Find character-level differences
			patches := dmp.DiffMain(oldLine.Content, newLine.Content, false)
			patches = dmp.DiffCleanupSemantic(patches)
			patches = dmp.DiffCleanupMerge(patches)
			patches = dmp.DiffCleanupEfficiency(patches)

			segments := make([]Segment, 0)

			removeStart := 0
			addStart := 0
			for _, patch := range patches {
				switch patch.Type {
				case diffmatchpatch.DiffDelete:
					segments = append(segments, Segment{
						Start: removeStart,
						End:   removeStart + len(patch.Text),
						Type:  LineRemoved,
						Text:  patch.Text,
					})
					removeStart += len(patch.Text)
				case diffmatchpatch.DiffInsert:
					segments = append(segments, Segment{
						Start: addStart,
						End:   addStart + len(patch.Text),
						Type:  LineAdded,
						Text:  patch.Text,
					})
					addStart += len(patch.Text)
				default:
					// Context text, no highlighting needed
					removeStart += len(patch.Text)
					addStart += len(patch.Text)
				}
			}
			oldLine.Segments = segments
			newLine.Segments = segments

			updated = append(updated, oldLine, newLine)
			i++ // Skip the next line as we've already processed it
		} else {
			updated = append(updated, h.Lines[i])
		}
	}

	h.Lines = updated
}

// pairLines converts a flat list of diff lines to pairs for side-by-side display
func pairLines(lines []DiffLine) []linePair {
	var pairs []linePair
	i := 0

	for i < len(lines) {
		switch lines[i].Kind {
		case LineRemoved:
			// Check if the next line is an addition, if so pair them
			if i+1 < len(lines) && lines[i+1].Kind == LineAdded {
				pairs = append(pairs, linePair{left: &lines[i], right: &lines[i+1]})
				i += 2
			} else {
				pairs = append(pairs, linePair{left: &lines[i], right: nil})
				i++
			}
		case LineAdded:
			pairs = append(pairs, linePair{left: nil, right: &lines[i]})
			i++
		case LineContext:
			pairs = append(pairs, linePair{left: &lines[i], right: &lines[i]})
			i++
		}
	}

	return pairs
}

// -------------------------------------------------------------------------
// Rendering Functions
// -------------------------------------------------------------------------

// renderLeftColumn formats the left side of a side-by-side diff
func renderLeftColumn(fileName string, dl *DiffLine, colWidth int) string {
	if dl == nil {
		return fmt.Sprintf("%*s", colWidth, "")
	}

	// Format line number
	lineNum := ""
	if dl.OldLineNo > 0 {
		lineNum = fmt.Sprintf("%6d", dl.OldLineNo)
	}

	var marker string
	switch dl.Kind {
	case LineRemoved:
		marker = "-"
	case LineAdded:
		marker = "?"
	case LineContext:
		marker = " "
	}

	content := dl.Content
	if len(content) > colWidth-10 {
		content = content[:colWidth-13] + "..."
	}

	return fmt.Sprintf("%s %s %s", lineNum, marker, content)
}

// renderRightColumn formats the right side of a side-by-side diff
func renderRightColumn(fileName string, dl *DiffLine, colWidth int) string {
	if dl == nil {
		return fmt.Sprintf("%*s", colWidth, "")
	}

	// Format line number
	lineNum := ""
	if dl.NewLineNo > 0 {
		lineNum = fmt.Sprintf("%6d", dl.NewLineNo)
	}

	var marker string
	switch dl.Kind {
	case LineAdded:
		marker = "+"
	case LineRemoved:
		marker = "?"
	case LineContext:
		marker = " "
	}

	content := dl.Content
	if len(content) > colWidth-10 {
		content = content[:colWidth-13] + "..."
	}

	return fmt.Sprintf("%s %s %s", lineNum, marker, content)
}

// -------------------------------------------------------------------------
// Public API
// -------------------------------------------------------------------------

// RenderSideBySideHunk formats a hunk for side-by-side display
func RenderSideBySideHunk(fileName string, h Hunk, opts ...SideBySideOption) string {
	// Apply options to create the configuration
	config := NewSideBySideConfig(opts...)

	// Make a copy of the hunk so we don't modify the original
	hunkCopy := Hunk{Lines: make([]DiffLine, len(h.Lines))}
	copy(hunkCopy.Lines, h.Lines)

	// Highlight changes within lines
	HighlightIntralineChanges(&hunkCopy)

	// Pair lines for side-by-side display
	pairs := pairLines(hunkCopy.Lines)

	// Calculate column width
	colWidth := config.TotalWidth / 2

	leftWidth := colWidth
	rightWidth := config.TotalWidth - colWidth
	var sb strings.Builder
	for _, p := range pairs {
		leftStr := renderLeftColumn(fileName, p.left, leftWidth)
		rightStr := renderRightColumn(fileName, p.right, rightWidth)
		sb.WriteString(leftStr + " | " + rightStr + "\n")
	}

	return sb.String()
}

// FormatDiff creates a side-by-side formatted view of a diff
func FormatDiff(diffText string, opts ...SideBySideOption) (string, error) {
	diffResult, err := ParseUnifiedDiff(diffText)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, h := range diffResult.Hunks {
		sb.WriteString(RenderSideBySideHunk(diffResult.OldFile, h, opts...))
	}

	return sb.String(), nil
}

// GenerateDiff creates a unified diff from two file contents
func GenerateDiff(beforeContent, afterContent, fileName string) (string, int, int) {
	// remove the cwd prefix and ensure consistent path format
	// this prevents issues with absolute paths in different environments
	cwd := config.WorkingDirectory()
	fileName = strings.TrimPrefix(fileName, cwd)
	fileName = strings.TrimPrefix(fileName, "/")

	var (
		unified   string
		additions int
		removals  int
	)

	// Use github.com/pmezard/go-difflib/difflib for unified diff generation
	// SplitLines helper if difflib doesn't have one (it expects slice of strings)
	splitLines := func(s string) []string {
		if s == "" {
			return []string{}
		}
		// Use SplitAfter to keep newlines, as difflib expects lines with newlines
		// if we want the output to have newlines in the body.
		return strings.SplitAfter(s, "\n")
	}

	diff := difflib.UnifiedDiff{
		A:        splitLines(beforeContent),
		B:        splitLines(afterContent),
		FromFile: "a/" + fileName,
		ToFile:   "b/" + fileName,
		Context:  3,
	}

	// GetUnifiedDiffString handles formatting
	unified, _ = difflib.GetUnifiedDiffString(diff)

	// Calculate additions and removals
	// Unified diff lines start with +, -, space, or @
	lines := strings.Split(unified, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			additions++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removals++
		}
	}

	return unified, additions, removals
}
