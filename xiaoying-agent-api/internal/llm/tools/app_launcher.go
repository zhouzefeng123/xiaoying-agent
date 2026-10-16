package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
)

const (
	AppLauncherToolName    = "app_launcher"
	appLauncherDescription = `打开 Windows 应用程序、文件、文件夹或网页。
使用说明:
- app_name: 想要启动的应用名称（如 'Chrome'、'QQ音乐'）、桌面文件名（如 '报告'）或绝对路径。如果同时指定 url，会用该应用打开网址。
- url: （可选）要在浏览器中打开的网址（如 'https://www.baidu.com'、'http://localhost:8080'）。
  - 如果同时指定 app_name 和 url：用 app_name 指定的浏览器打开该网址（如 app_name='Chrome' url='http://localhost:8080'）。
  - 如果只指定 url 不指定 app_name：用默认浏览器打开该网址。
功能特点:
- 自动识别路径或URL，直接打开文件、文件夹或网页。
- 扫描桌面和开始菜单快捷方式，支持模糊匹配。
- 视觉搜索回退：截图后通过视觉模型定位图标并模拟点击。
限制: 仅支持 Windows 系统。
重要: 此工具为一次性操作。调用成功后直接告知用户结果即可，不要重复调用。不要同时调用此工具和 run_command 来打开同一应用或网页。
`
)

type AppLauncherParams struct {
	AppName string `json:"app_name"`
	URL     string `json:"url"`
}

type appLauncherTool struct {
	apiKey string
}

func NewAppLauncherTool() BaseTool {
	// 尝试从配置中获取 API Key，用于视觉搜索回退
	var apiKey string
	if cfg := config.Get(); cfg != nil {
		if p, ok := cfg.Providers[models.ProviderAliyun]; ok && p.APIKey != "" {
			apiKey = p.APIKey
		}
	}
	if apiKey == "" {
		apiKey = os.Getenv("DASHSCOPE_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("ALIYUN_API_KEY")
	}
	return &appLauncherTool{apiKey: apiKey}
}

func (t *appLauncherTool) Info() ToolInfo {
	return ToolInfo{
		Name:        AppLauncherToolName,
		Description: appLauncherDescription,
		Parameters: map[string]any{
			"app_name": map[string]any{
				"type":        "string",
				"description": "想要启动的应用程序名称（如 'Chrome'、'QQ音乐'）、桌面文件名（如 '报告'）或文件/文件夹的绝对路径。",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "要在浏览器中打开的网址。如果同时指定 app_name，会用该应用打开网址；如果只指定 url，用默认浏览器打开。",
			},
		},
		Required: []string{"app_name"},
	}
}

func (t *appLauncherTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	if runtime.GOOS != "windows" {
		return NewTextErrorResponse("app_launcher 仅支持 Windows 系统"), nil
	}

	var params AppLauncherParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("参数解析失败: %v", err)), nil
	}

	// Case 1: Both app_name and url provided → launch the specified app with the URL
	// e.g. app_launcher(app_name="Chrome", url="http://localhost:8080") → open Chrome with URL
	if params.AppName != "" && params.URL != "" {
		result := t.launchAppWithURL(params.AppName, params.URL)
		if strings.HasPrefix(result, "Error:") || strings.HasPrefix(result, "Failed") {
			return NewTextErrorResponse(result), nil
		}
		return NewTextResponse(result), nil
	}

	// Case 2: Only url provided (app_name empty) → open in default browser
	if params.URL != "" {
		result := openURL(params.URL)
		if strings.HasPrefix(result, "Error:") || strings.HasPrefix(result, "Failed") {
			return NewTextErrorResponse(result), nil
		}
		return NewTextResponse(result), nil
	}

	// Case 3: Only app_name provided
	if params.AppName == "" {
		return NewTextErrorResponse("app_name 参数不能为空"), nil
	}

	// If app_name itself is a URL, open it directly in the default browser
	if isURL(params.AppName) {
		result := openURL(params.AppName)
		if strings.HasPrefix(result, "Error:") || strings.HasPrefix(result, "Failed") {
			return NewTextErrorResponse(result), nil
		}
		return NewTextResponse(result), nil
	}

	result := t.findAndLaunch(params.AppName)
	if strings.HasPrefix(result, "Error:") || strings.HasPrefix(result, "Failed") {
		return NewTextErrorResponse(result), nil
	}
	return NewTextResponse(result), nil
}

// findAndLaunch 查找并启动应用
func (t *appLauncherTool) findAndLaunch(appName string) string {
	// 策略 0: 直接路径
	if _, err := os.Stat(appName); err == nil {
		msg := openByPath(appName)
		return msg
	}

	// 策略 1: 扫描桌面
	if result := searchDesktop(appName); result != "" {
		return result
	}

	// 策略 2: 扫描开始菜单快捷方式
	if result := searchStartMenu(appName); result != "" {
		return result
	}

	// 策略 3: 视觉搜索回退（截图 + Qwen-VL 定位 + 模拟点击）
	if t.apiKey != "" {
		if result := t.visualSearchAndClick(appName); result != "" {
			return result
		}
	}

	return fmt.Sprintf("Error: 未找到应用 '%s'。已尝试: 1)路径直接打开 2)扫描桌面 3)搜索开始菜单快捷方式。请让用户提供完整路径或确认应用名称。", appName)
}

// openByPath 通过路径打开文件/文件夹
func openByPath(path string) string {
	absPath, _ := filepath.Abs(path)

	// 使用 PowerShell Start-Process，隐藏窗口
	err := runHiddenPowerShell(fmt.Sprintf(`Start-Process "%s"`, absPath), 10*time.Second)
	if err != nil {
		// 回退到 os.startfile 等效操作
		cmd := exec.Command("cmd", "/c", "start", "", absPath)
		cmd.SysProcAttr = hiddenWindowAttr()
		if err2 := cmd.Run(); err2 != nil {
			return fmt.Sprintf("Failed: 无法打开 '%s': %v", path, err2)
		}
	}

	// 判断目标类型，提供更丰富的信息
	fi, _ := os.Stat(absPath)
	targetType := "文件"
	if fi != nil && fi.IsDir() {
		targetType = "文件夹"
	}
	return fmt.Sprintf("已成功打开%s: %s。操作完成，请告知用户。", targetType, absPath)
}

// isURL 判断字符串是否为 URL
func isURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "ftp://")
}

// openURL 在默认浏览器中打开 URL
func openURL(url string) string {
	// 使用 cmd /c start 打开 URL，Windows 会自动使用默认浏览器
	cmd := exec.Command("cmd", "/c", "start", "", url)
	cmd.SysProcAttr = hiddenWindowAttr()
	if err := cmd.Run(); err != nil {
		// 回退到 PowerShell
		psErr := runHiddenPowerShell(fmt.Sprintf(`Start-Process "%s"`, url), 10*time.Second)
		if psErr != nil {
			return fmt.Sprintf("Error: 无法打开网址 '%s': %v", url, err)
		}
	}
	return fmt.Sprintf("已在默认浏览器中打开网址: %s。操作完成，请告知用户。", url)
}

// launchAppWithURL 用指定应用打开 URL
// 核心场景: app_launcher(app_name="Chrome", url="http://localhost:8080") → 用 Chrome 打开该网址
func (t *appLauncherTool) launchAppWithURL(appName, url string) string {
	// 策略 1: 如果 app_name 是已知浏览器的简称，直接用 start 命令打开
	browserAliases := map[string]string{
		"chrome":          "chrome",
		"google chrome":   "chrome",
		"firefox":         "firefox",
		"mozilla firefox": "firefox",
		"edge":            "msedge",
		"microsoft edge":  "msedge",
		"brave":           "brave",
	}
	if browserCmd, ok := browserAliases[strings.ToLower(appName)]; ok {
		// Use PowerShell Start-Process with -ArgumentList for URL
		psCmd := fmt.Sprintf(`Start-Process "%s" -ArgumentList '"%s"'`, browserCmd, url)
		if err := runHiddenPowerShell(psCmd, 10*time.Second); err != nil {
			// Fallback: cmd /c start browser "url"
			cmd := exec.Command("cmd", "/c", "start", "", browserCmd, url)
			cmd.SysProcAttr = hiddenWindowAttr()
			if err2 := cmd.Run(); err2 != nil {
				return fmt.Sprintf("Error: 无法用 %s 打开网址 '%s': %v", appName, url, err)
			}
		}
		return fmt.Sprintf("已用 %s 打开网址: %s。操作完成，请告知用户。", appName, url)
	}

	// 策略 2: app_name 是文件路径 → 直接用 Start-Process path URL
	if _, err := os.Stat(appName); err == nil {
		psCmd := fmt.Sprintf(`Start-Process "%s" -ArgumentList '"%s"'`, appName, url)
		if err := runHiddenPowerShell(psCmd, 10*time.Second); err != nil {
			cmd := exec.Command("cmd", "/c", "start", "", "\""+appName+"\"", url)
			cmd.SysProcAttr = hiddenWindowAttr()
			if err2 := cmd.Run(); err2 != nil {
				return fmt.Sprintf("Error: 无法用 %s 打开网址 '%s': %v", appName, url, err)
			}
		}
		return fmt.Sprintf("已用 %s 打开网址: %s。操作完成，请告知用户。", appName, url)
	}

	// 策略 3: 扫描桌面和开始菜单找到应用 .lnk 路径，再用该路径打开 URL
	appPath := findAppPath(appName)
	if appPath != "" {
		// .lnk files are launched via cmd /c start which passes arguments through
		cmd := exec.Command("cmd", "/c", "start", "", "\""+appPath+"\"", url)
		cmd.SysProcAttr = hiddenWindowAttr()
		if err := cmd.Run(); err != nil {
			psCmd := fmt.Sprintf(`Start-Process "%s" -ArgumentList '"%s"'`, appPath, url)
			if psErr := runHiddenPowerShell(psCmd, 10*time.Second); psErr != nil {
				return fmt.Sprintf("Error: 无法用 %s 打开网址 '%s': %v", appName, url, err)
			}
		}
		return fmt.Sprintf("已用 %s 打开网址: %s。操作完成，请告知用户。", appName, url)
	}

	// 策略 4: 找不到应用，回退到默认浏览器打开 URL
	return openURL(url)
}

// searchDesktop 扫描桌面文件
func searchDesktop(appName string) string {
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		return ""
	}
	desktopPath := filepath.Join(userProfile, "Desktop")
	if _, err := os.Stat(desktopPath); err != nil {
		return ""
	}

	entries, err := os.ReadDir(desktopPath)
	if err != nil {
		return ""
	}

	appNameLower := strings.ToLower(appName)

	// 精确匹配优先
	var exactMatches, partialMatches []string
	for _, entry := range entries {
		nameLower := strings.ToLower(entry.Name())
		nameNoExt := strings.TrimSuffix(nameLower, filepath.Ext(nameLower))

		if nameNoExt == appNameLower || nameLower == appNameLower {
			exactMatches = append(exactMatches, entry.Name())
		} else if strings.Contains(nameLower, appNameLower) {
			partialMatches = append(partialMatches, entry.Name())
		}
	}

	var target string
	if len(exactMatches) > 0 {
		target = exactMatches[0]
	} else if len(partialMatches) > 0 {
		// 选最短的部分匹配
		shortest := partialMatches[0]
		for _, m := range partialMatches[1:] {
			if len(m) < len(shortest) {
				shortest = m
			}
		}
		target = shortest
	}

	if target != "" {
		fullPath := filepath.Join(desktopPath, target)
		return openByPath(fullPath)
	}

	return ""
}

// searchStartMenu 扫描开始菜单快捷方式
func searchStartMenu(appName string) string {
	searchPaths := []string{
		filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs"),
		filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs"),
	}

	type lnkEntry struct {
		name string // 不含扩展名的小写名称
		path string
	}
	var lnkFiles []lnkEntry

	for _, base := range searchPaths {
		if _, err := os.Stat(base); err != nil {
			continue
		}
		filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".lnk") {
				nameNoExt := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
				lnkFiles = append(lnkFiles, lnkEntry{name: strings.ToLower(nameNoExt), path: path})
			}
			return nil
		})
	}

	if len(lnkFiles) == 0 {
		return ""
	}

	appNameLower := strings.ToLower(appName)

	// 1. 精确匹配
	for _, lnk := range lnkFiles {
		if lnk.name == appNameLower {
			return launchLnk(lnk.path, lnk.name)
		}
	}

	// 2. 包含匹配
	for _, lnk := range lnkFiles {
		if strings.Contains(lnk.name, appNameLower) {
			return launchLnk(lnk.path, lnk.name)
		}
	}

	// 3. 模糊匹配（基于编辑距离）
	bestIdx := -1
	bestScore := 0
	for i, lnk := range lnkFiles {
		score := fuzzyScore(appNameLower, lnk.name)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	// 阈值: 至少匹配 40% 的字符
	if bestIdx >= 0 && bestScore*100/utf8.RuneCountInString(appNameLower) >= 40 {
		return launchLnk(lnkFiles[bestIdx].path, lnkFiles[bestIdx].name)
	}

	return ""
}

// findAppPath 查找应用的 .lnk 路径（不启动，仅返回路径）
// 用于 launchAppWithURL 中找到应用后带 URL 参数启动
func findAppPath(appName string) string {
	appNameLower := strings.ToLower(appName)

	// 搜索桌面
	userProfile := os.Getenv("USERPROFILE")
	if userProfile != "" {
		desktopPath := filepath.Join(userProfile, "Desktop")
		if entries, err := os.ReadDir(desktopPath); err == nil {
			for _, entry := range entries {
				nameLower := strings.ToLower(entry.Name())
				nameNoExt := strings.TrimSuffix(nameLower, filepath.Ext(nameLower))
				if nameNoExt == appNameLower || nameLower == appNameLower {
					return filepath.Join(desktopPath, entry.Name())
				}
			}
			for _, entry := range entries {
				nameLower := strings.ToLower(entry.Name())
				if strings.Contains(nameLower, appNameLower) {
					return filepath.Join(desktopPath, entry.Name())
				}
			}
		}
	}

	// 搜索开始菜单
	searchPaths := []string{
		filepath.Join(os.Getenv("ProgramData"), "Microsoft", "Windows", "Start Menu", "Programs"),
		filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs"),
	}
	for _, base := range searchPaths {
		if _, err := os.Stat(base); err != nil {
			continue
		}
		var foundPath string
		filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil || foundPath != "" {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".lnk") {
				nameNoExt := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
				if strings.ToLower(nameNoExt) == appNameLower {
					foundPath = path
				}
			}
			return nil
		})
		if foundPath != "" {
			return foundPath
		}
	}

	return ""
}

// launchLnk 启动快捷方式
func launchLnk(lnkPath, displayName string) string {
	cmd := exec.Command("cmd", "/c", "start", "", lnkPath)
	cmd.SysProcAttr = hiddenWindowAttr()
	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("Error: 找到快捷方式 '%s' 但启动失败: %v", displayName, err)
	}
	return fmt.Sprintf("已成功启动应用: %s (快捷方式: %s)。操作完成，请告知用户。", displayName, lnkPath)
}

// runHiddenPowerShell 隐藏窗口执行 PowerShell 命令
func runHiddenPowerShell(command string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.SysProcAttr = hiddenWindowAttr()
	return cmd.Run()
}

// fuzzyScore 计算两个字符串的模糊匹配分（返回匹配的字符数）
func fuzzyScore(query, target string) int {
	qRunes := []rune(query)
	tRunes := []rune(target)
	qi := 0
	for ti := 0; ti < len(tRunes) && qi < len(qRunes); ti++ {
		if qRunes[qi] == tRunes[ti] {
			qi++
		}
	}
	return qi
}

// === 视觉搜索回退 ===

// visualSearchAndClick 截图后调用 Qwen-VL 定位应用图标并模拟点击
func (t *appLauncherTool) visualSearchAndClick(appName string) string {
	// 1. 截图
	tempImg := filepath.Join(os.TempDir(), "auramate_launcher_snap.png")
	if err := takeScreenshot(tempImg); err != nil {
		return ""
	}
	defer os.Remove(tempImg)

	// 2. 编码图片
	imgData, err := os.ReadFile(tempImg)
	if err != nil {
		return ""
	}
	base64Img := fmt.Sprintf("data:image/png;base64,%s", base64.StdEncoding.EncodeToString(imgData))

	// 3. 调用 Qwen-VL 定位
	prompt := fmt.Sprintf("Detect the icon for application '%s' on the desktop or taskbar. Return the bounding box coordinates in [ymin, xmin, ymax, xmax] format (0-1000 normalized).", appName)

	content, err := t.callQwenVL(base64Img, prompt)
	if err != nil {
		return ""
	}

	// 4. 解析坐标
	coordRegex := regexp.MustCompile(`\d+`)
	coords := coordRegex.FindAllString(content, 4)
	if len(coords) < 4 {
		return ""
	}

	y1, _ := strconv.Atoi(coords[0])
	x1, _ := strconv.Atoi(coords[1])
	y2, _ := strconv.Atoi(coords[2])
	x2, _ := strconv.Atoi(coords[3])

	// 5. 获取屏幕分辨率并计算点击坐标
	screenW, screenH := getScreenSize()
	if screenW == 0 || screenH == 0 {
		return ""
	}

	centerX := ((x1 + x2) / 2) * screenW / 1000
	centerY := ((y1 + y2) / 2) * screenH / 1000

	// 6. 双击（桌面图标需要双击）
	clickAt(centerX, centerY)
	time.Sleep(100 * time.Millisecond)
	clickAt(centerX, centerY)

	return fmt.Sprintf("已通过视觉搜索定位并双击了 '%s'（屏幕坐标 %d, %d）。操作完成，请告知用户。", appName, centerX, centerY)
}

// takeScreenshot 使用 PowerShell 截取全屏
func takeScreenshot(path string) error {
	psScript := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms;
Add-Type -AssemblyName System.Drawing;
$s = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds;
$b = New-Object System.Drawing.Bitmap($s.Width, $s.Height);
$g = [System.Drawing.Graphics]::FromImage($b);
$g.CopyFromScreen(0, 0, 0, 0, $b.Size);
$b.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png);
$g.Dispose();
$b.Dispose();
`, strings.ReplaceAll(path, `'`, `''`))

	return runHiddenPowerShell(psScript, 10*time.Second)
}

// getScreenSize 通过 PowerShell 获取屏幕分辨率
func getScreenSize() (int, int) {
	psCmd := `
Add-Type -AssemblyName System.Windows.Forms;
$s = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds;
Write-Output "$($s.Width),$($s.Height)"
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
	cmd.SysProcAttr = hiddenWindowAttr()
	out, err := cmd.Output()
	if err != nil {
		return 0, 0
	}

	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	return w, h
}

// clickAt 通过 PowerShell 模拟鼠标点击
func clickAt(x, y int) {
	psCmd := fmt.Sprintf(`
Add-Type @"
using System;
using System.Runtime.InteropServices;
public class Mouse {
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int X, int Y);
    [DllImport("user32.dll")] public static extern void mouse_event(uint dwFlags, uint dx, uint dy, uint dwData, int dwExtraInfo);
}
"@;
[Mouse]::SetCursorPos(%d, %d);
[Mouse]::mouse_event(0x0002, 0, 0, 0, 0);
[Mouse]::mouse_event(0x0004, 0, 0, 0, 0);
`, x, y)

	_ = runHiddenPowerShell(psCmd, 5*time.Second)
}

// callQwenVL 调用阿里云 Qwen-VL 模型
func (t *appLauncherTool) callQwenVL(base64Image, prompt string) (string, error) {
	url := "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"

	payload := map[string]any{
		"model": "qwen-vl-plus",
		"input": map[string]any{
			"messages": []map[string]any{
				{
					"role": "user",
					"content": []map[string]any{
						{"image": base64Image},
						{"text": prompt},
					},
				},
			},
		},
		"parameters": map[string]any{},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", t.apiKey))
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error: status=%d", resp.StatusCode)
	}

	var result struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if len(result.Output.Choices) > 0 && len(result.Output.Choices[0].Message.Content) > 0 {
		return result.Output.Choices[0].Message.Content[0].Text, nil
	}
	return "", fmt.Errorf("empty response")
}
