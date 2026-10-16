package tools

import (
	"bytes"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestWrapReaderDecodesGBKOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only decoding path")
	}

	decoded, err := io.ReadAll(wrapReader(bytes.NewReader([]byte{0xD6, 0xD0, 0xCE, 0xC4, '\r', '\n'})))
	if err != nil {
		t.Fatalf("read decoded output: %v", err)
	}

	if string(decoded) != "中文\r\n" {
		t.Fatalf("unexpected decoded text: %q", string(decoded))
	}
}

func TestWrapReaderPreservesUTF8OnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only decoding path")
	}

	decoded, err := io.ReadAll(wrapReader(bytes.NewReader([]byte("中文\r\n"))))
	if err != nil {
		t.Fatalf("read decoded output: %v", err)
	}

	if string(decoded) != "中文\r\n" {
		t.Fatalf("unexpected decoded text: %q", string(decoded))
	}
}

func TestWrapReaderDecodesUTF16WithBOMOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only decoding path")
	}

	utf16LE := []byte{0xFF, 0xFE, 0x2D, 0x4E, 0x87, 0x65, 0x0D, 0x00, 0x0A, 0x00}
	decoded, err := io.ReadAll(wrapReader(bytes.NewReader(utf16LE)))
	if err != nil {
		t.Fatalf("read decoded output: %v", err)
	}

	if string(decoded) != "中文\r\n" {
		t.Fatalf("unexpected decoded text: %q", string(decoded))
	}
}

func TestWrapReaderDecodesUTF16ASCIIWithoutBOMOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only decoding path")
	}

	utf16LE := []byte{'A', 0x00, 'B', 0x00, '\r', 0x00, '\n', 0x00}
	decoded, err := io.ReadAll(wrapReader(bytes.NewReader(utf16LE)))
	if err != nil {
		t.Fatalf("read decoded output: %v", err)
	}

	if string(decoded) != "AB\r\n" {
		t.Fatalf("unexpected decoded text: %q", string(decoded))
	}
}

func TestApplyCommandEnvAddsPythonUTF8Defaults(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only env defaults")
	}

	cmd := exec.Command("cmd")
	applyCommandEnv(cmd, nil)

	env := make(map[string]string)
	for _, item := range cmd.Env {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}

	if env["PYTHONIOENCODING"] != "utf-8" {
		t.Fatalf("unexpected PYTHONIOENCODING: %q", env["PYTHONIOENCODING"])
	}
	if env["PYTHONUTF8"] != "1" {
		t.Fatalf("unexpected PYTHONUTF8: %q", env["PYTHONUTF8"])
	}
}
