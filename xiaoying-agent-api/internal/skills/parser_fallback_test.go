package skills

import (
	"os"
	"testing"
)

func TestScanArgparseDefinitions(t *testing.T) {
	content := `
import argparse
parser = argparse.ArgumentParser()
parser.add_argument('--name', help='Name param')
parser.add_argument("--count", type=int, default=1, help="Count param")
parser.add_argument('--verbose', action='store_true', help='Verbose flag')
`
	tmpFile, err := os.CreateTemp("", "test_script_*.py")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	params, required := scanArgparseDefinitions(tmpFile.Name())

	if len(required) != 0 {
		t.Errorf("Expected 0 required, got %d", len(required))
	}

	if len(params) != 3 {
		t.Errorf("Expected 3 params, got %d", len(params))
	}

	if _, ok := params["name"]; !ok {
		t.Error("Missing 'name' param")
	}
	if _, ok := params["count"]; !ok {
		t.Error("Missing 'count' param")
	}
	if _, ok := params["verbose"]; !ok {
		t.Error("Missing 'verbose' param")
	}

	// Check type of verbose
	if p, ok := params["verbose"].(map[string]string); ok {
		if p["type"] != "boolean" {
			t.Errorf("verbose should be boolean, got %s", p["type"])
		}
	} else {
		t.Error("verbose param format error")
	}
}
