package utils

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

type SanitizeResult struct {
	Content      string
	WasModified  bool
	DetectedType string
}

func SanitizeUTF8(content string) SanitizeResult {
	return SanitizeUTF8WithSource(content, "")
}

func SanitizeUTF8WithSource(content string, source string) SanitizeResult {
	result := SanitizeResult{
		Content:      content,
		WasModified:  false,
		DetectedType: "utf-8",
	}

	if content == "" {
		return result
	}

	if utf8.ValidString(content) {
		return result
	}

	result.WasModified = true
	rawBytes := []byte(content)

	if isBinaryContent(rawBytes) {
		result.DetectedType = "binary"
		if source != "" {
			result.Content = fmt.Sprintf("[检测到二进制内容，无法以文本形式显示。来源: %s]", source)
		} else {
			result.Content = "[检测到二进制内容，无法以文本形式显示]"
		}
		return result
	}

	if converted, ok := tryConvertGBK(rawBytes); ok {
		result.DetectedType = "gbk"
		result.Content = converted
		return result
	}

	if converted, ok := tryConvertBIG5(rawBytes); ok {
		result.DetectedType = "big5"
		result.Content = converted
		return result
	}

	result.DetectedType = "invalid-utf8"
	result.Content = strings.ToValidUTF8(content, "�")
	return result
}

func SanitizeBytes(data []byte, source string) SanitizeResult {
	content := string(data)
	return SanitizeUTF8WithSource(content, source)
}

func isBinaryContent(data []byte) bool {
	checkLen := len(data)
	if checkLen == 0 {
		return false
	}
	if checkLen > 512 {
		checkLen = 512
	}

	nullCount := 0
	controlCount := 0

	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			nullCount++
		} else if data[i] < 32 && data[i] != 9 && data[i] != 10 && data[i] != 13 {
			controlCount++
		}
	}

	if float64(nullCount)/float64(checkLen) > 0.01 {
		return true
	}

	if float64(controlCount)/float64(checkLen) > 0.3 {
		return true
	}

	return false
}

func tryConvertGBK(data []byte) (string, bool) {
	if !bytesLikelyGBK(data) {
		return "", false
	}

	reader := transform.NewReader(bytes.NewReader(data), simplifiedchinese.GBK.NewDecoder())
	converted, err := io.ReadAll(reader)
	if err != nil {
		return "", false
	}

	result := string(converted)
	if !isValidTextContent(result) {
		return "", false
	}

	return result, true
}

func bytesLikelyGBK(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	gbkLikely := 0
	total := 0

	for i := 0; i < len(data); {
		if data[i] <= 0x7F {
			i++
			continue
		}

		if i+1 < len(data) {
			b1, b2 := data[i], data[i+1]
			if (b1 >= 0x81 && b1 <= 0xFE) && ((b2 >= 0x40 && b2 <= 0x7E) || (b2 >= 0xA1 && b2 <= 0xFE)) {
				gbkLikely++
				i += 2
			} else {
				i++
			}
			total++
		} else {
			break
		}
	}

	if total == 0 {
		return false
	}

	return float64(gbkLikely)/float64(total) > 0.7
}

func tryConvertBIG5(data []byte) (string, bool) {
	if !bytesLikelyBIG5(data) {
		return "", false
	}

	reader := transform.NewReader(bytes.NewReader(data), traditionalchinese.Big5.NewDecoder())
	converted, err := io.ReadAll(reader)
	if err != nil {
		return "", false
	}

	result := string(converted)
	if !isValidTextContent(result) {
		return "", false
	}

	return result, true
}

func bytesLikelyBIG5(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	big5Likely := 0
	total := 0

	for i := 0; i < len(data); {
		if data[i] <= 0x7F {
			i++
			continue
		}

		if i+1 < len(data) {
			b1, b2 := data[i], data[i+1]
			if (b1 >= 0x81 && b1 <= 0xFE) && ((b2 >= 0x40 && b2 <= 0x7E) || (b2 >= 0xA1 && b2 <= 0xFE)) {
				big5Likely++
				i += 2
			} else {
				i++
			}
			total++
		} else {
			break
		}
	}

	if total == 0 {
		return false
	}

	return float64(big5Likely)/float64(total) > 0.7
}

func isValidTextContent(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}

	if len(s) == 0 {
		return false
	}

	printable := 0
	total := 0
	for _, r := range s {
		total++
		if r >= 32 || r == '\n' || r == '\r' || r == '\t' {
			printable++
		}
	}

	if total == 0 {
		return false
	}

	return float64(printable)/float64(total) > 0.5
}

func SanitizeUserInput(content string) string {
	if utf8.ValidString(content) {
		return content
	}

	result := SanitizeUTF8(content)
	return result.Content
}

func IsBinaryFile(data []byte) bool {
	return isBinaryContent(data)
}
