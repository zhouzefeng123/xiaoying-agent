package common

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

var (
	machineCodeOnce sync.Once
	machineCode     string
)

// GetMachineCode returns a stable device identifier derived from the current machine.
// The value is resolved on demand and cached only in memory for the current process.
func GetMachineCode() string {
	machineCodeOnce.Do(func() {
		machineCode = resolveMachineCode()
	})
	return machineCode
}

func resolveMachineCode() string {
	raw := strings.TrimSpace(readPlatformMachineID())
	if raw == "" {
		hostname, err := os.Hostname()
		if err == nil {
			raw = strings.TrimSpace(hostname)
		}
	}
	if raw == "" {
		return ""
	}

	hash := sha256.Sum256([]byte(strings.ToLower(raw)))
	encoded := hex.EncodeToString(hash[:16])
	return formatUUIDLike(encoded)
}

func readPlatformMachineID() string {
	switch runtime.GOOS {
	case "windows":
		return firstNonEmpty(
			runCommand("reg", "query", `HKLM\\SOFTWARE\\Microsoft\\Cryptography`, "/v", "MachineGuid"),
			runCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", `(Get-ItemProperty -Path 'HKLM:\\SOFTWARE\\Microsoft\\Cryptography' -Name 'MachineGuid' -ErrorAction SilentlyContinue).MachineGuid`),
		)
	case "darwin":
		return runCommand("ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
	default:
		return firstNonEmpty(
			readFileTrimmed("/etc/machine-id"),
			readFileTrimmed("/var/lib/dbus/machine-id"),
		)
	}
}

func runCommand(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return extractMachineID(string(out))
}

func readFileTrimmed(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return extractMachineID(string(content))
}

func extractMachineID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	for _, line := range strings.Split(trimmed, "\n") {
		cleanLine := strings.TrimSpace(line)
		if cleanLine == "" {
			continue
		}
		fields := strings.Fields(cleanLine)
		lastField := fields[len(fields)-1]
		if looksLikeMachineID(lastField) {
			return lastField
		}
		if strings.Contains(cleanLine, "IOPlatformUUID") {
			if start := strings.Index(cleanLine, `"`); start >= 0 {
				if end := strings.LastIndex(cleanLine, `"`); end > start {
					candidate := cleanLine[start+1 : end]
					if looksLikeMachineID(candidate) {
						return candidate
					}
				}
			}
		}
	}

	return trimmed
}

func looksLikeMachineID(value string) bool {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	if value == "" {
		return false
	}
	if strings.EqualFold(value, "MachineGuid") || strings.EqualFold(value, "REG_SZ") {
		return false
	}
	return len(value) >= 8
}

func formatUUIDLike(hexValue string) string {
	if len(hexValue) < 32 {
		return hexValue
	}
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
