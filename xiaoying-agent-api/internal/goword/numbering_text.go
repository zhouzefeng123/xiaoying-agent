package goword

import (
	"fmt"
	"strconv"
	"strings"
)

type numberingSequenceState struct {
	values map[string][]int
}

func applyResolvedNumbering(document *Document) {
	if document == nil {
		return
	}
	definitions := buildNumberingDefinitionIndex(document.Meta.Numbering)
	applyNumberingToBlocks(document.Blocks, definitions, &numberingSequenceState{values: make(map[string][]int)})
	document.rebuildParagraphs()
	for index := range document.Meta.HeaderFooters {
		applyNumberingToBlocks(document.Meta.HeaderFooters[index].Blocks, definitions, &numberingSequenceState{values: make(map[string][]int)})
		document.Meta.HeaderFooters[index].rebuildParagraphs()
	}
}

func buildNumberingDefinitionIndex(definitions []NumberingDefinition) map[string]map[int]NumberingLevel {
	index := make(map[string]map[int]NumberingLevel, len(definitions))
	for _, definition := range definitions {
		if strings.TrimSpace(definition.NumID) == "" {
			continue
		}
		levels := make(map[int]NumberingLevel, len(definition.Levels))
		for _, level := range definition.Levels {
			levels[level.Level] = level
		}
		index[definition.NumID] = levels
	}
	return index
}

func applyNumberingToBlocks(blocks []Block, definitions map[string]map[int]NumberingLevel, state *numberingSequenceState) {
	for blockIndex := range blocks {
		block := &blocks[blockIndex]
		switch block.Type {
		case BlockTypeParagraph:
			if block.Paragraph != nil {
				block.Paragraph.NumberingText = resolveParagraphNumberingText(block.Paragraph, definitions, state)
			}
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for rowIndex := range block.Table.Rows {
				for cellIndex := range block.Table.Rows[rowIndex].Cells {
					applyNumberingToBlocks(block.Table.Rows[rowIndex].Cells[cellIndex].Blocks, definitions, state)
				}
			}
		}
	}
}

func resolveParagraphNumberingText(paragraph *Paragraph, definitions map[string]map[int]NumberingLevel, state *numberingSequenceState) string {
	if paragraph == nil || paragraph.Style.Numbering == nil || strings.TrimSpace(paragraph.Style.Numbering.NumID) == "" {
		return ""
	}
	if state == nil {
		state = &numberingSequenceState{values: make(map[string][]int)}
	}
	levels, ok := definitions[paragraph.Style.Numbering.NumID]
	if !ok {
		return ""
	}
	levelIndex := paragraph.Style.Numbering.Level
	currentLevel, ok := levels[levelIndex]
	if !ok {
		return ""
	}
	if state.values == nil {
		state.values = make(map[string][]int)
	}
	counters := state.values[paragraph.Style.Numbering.NumID]
	if len(counters) <= levelIndex {
		expanded := make([]int, levelIndex+1)
		copy(expanded, counters)
		counters = expanded
	}
	start := currentLevel.Start
	if start <= 0 {
		start = 1
	}
	if counters[levelIndex] == 0 {
		counters[levelIndex] = start - 1
	}
	counters[levelIndex] += 1
	for index := levelIndex + 1; index < len(counters); index += 1 {
		counters[index] = 0
	}
	state.values[paragraph.Style.Numbering.NumID] = counters
	return renderNumberingText(currentLevel.Text, levelIndex, levels, counters)
}

func renderNumberingText(template string, currentLevel int, levels map[int]NumberingLevel, counters []int) string {
	trimmedTemplate := strings.TrimSpace(template)
	if trimmedTemplate == "" {
		return formatNumberingValue(effectiveNumberingValue(counters, currentLevel, levels), levels[currentLevel].Format)
	}
	out := template
	hasPlaceholder := false
	for index := 0; index < len(counters) && index < 9; index += 1 {
		placeholder := fmt.Sprintf("%%%d", index+1)
		if !strings.Contains(out, placeholder) {
			continue
		}
		hasPlaceholder = true
		out = strings.ReplaceAll(out, placeholder, formatNumberingValue(effectiveNumberingValue(counters, index, levels), levels[index].Format))
	}
	if hasPlaceholder {
		return out
	}
	return template
}

func effectiveNumberingValue(counters []int, level int, levels map[int]NumberingLevel) int {
	if level >= 0 && level < len(counters) && counters[level] > 0 {
		return counters[level]
	}
	if definition, ok := levels[level]; ok && definition.Start > 0 {
		return definition.Start
	}
	return 1
}

func formatNumberingValue(value int, format string) string {
	if value <= 0 {
		value = 1
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "decimal", "cardinaltext", "ordinal":
		return strconv.Itoa(value)
	case "decimalzero":
		if value < 10 {
			return fmt.Sprintf("0%d", value)
		}
		return strconv.Itoa(value)
	case "lowerletter":
		return toAlphabeticSequence(value, false)
	case "upperletter":
		return toAlphabeticSequence(value, true)
	case "lowerroman":
		return strings.ToLower(toRoman(value))
	case "upperroman":
		return toRoman(value)
	case "chinesecounting", "chineselegalsimplified", "chinesecountingthousand", "ideographdigital", "ideographtraditional":
		return toChineseCounting(value)
	default:
		return strconv.Itoa(value)
	}
}

func toAlphabeticSequence(value int, upper bool) string {
	if value <= 0 {
		return ""
	}
	letters := make([]byte, 0, 4)
	for value > 0 {
		value--
		letters = append([]byte{byte('a' + (value % 26))}, letters...)
		value /= 26
	}
	result := string(letters)
	if upper {
		return strings.ToUpper(result)
	}
	return result
}

func toRoman(value int) string {
	if value <= 0 {
		return ""
	}
	numerals := []struct {
		value  int
		symbol string
	}{
		{1000, "M"},
		{900, "CM"},
		{500, "D"},
		{400, "CD"},
		{100, "C"},
		{90, "XC"},
		{50, "L"},
		{40, "XL"},
		{10, "X"},
		{9, "IX"},
		{5, "V"},
		{4, "IV"},
		{1, "I"},
	}
	var builder strings.Builder
	for _, numeral := range numerals {
		for value >= numeral.value {
			builder.WriteString(numeral.symbol)
			value -= numeral.value
		}
	}
	return builder.String()
}

func toChineseCounting(value int) string {
	if value <= 0 {
		return strconv.Itoa(value)
	}
	digits := []string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}
	units := []struct {
		value int
		label string
	}{
		{1000, "千"},
		{100, "百"},
		{10, "十"},
	}
	if value < 10 {
		return digits[value]
	}
	remaining := value
	var builder strings.Builder
	zeroPending := false
	for _, unit := range units {
		current := remaining / unit.value
		remaining %= unit.value
		if current == 0 {
			if builder.Len() > 0 && remaining > 0 {
				zeroPending = true
			}
			continue
		}
		if zeroPending {
			builder.WriteString(digits[0])
			zeroPending = false
		}
		if unit.value == 10 && current == 1 && builder.Len() == 0 {
			builder.WriteString(unit.label)
		} else {
			builder.WriteString(digits[current])
			builder.WriteString(unit.label)
		}
	}
	if remaining > 0 {
		if zeroPending {
			builder.WriteString(digits[0])
		}
		builder.WriteString(digits[remaining])
	}
	return builder.String()
}
