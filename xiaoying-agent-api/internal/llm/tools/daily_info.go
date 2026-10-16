package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/permission"
)

const (
	DailyInfoToolName    = "daily_info"
	dailyInfoDescription = `获取天气预报或最新新闻。
使用说明:
- type: 'weather' (天气) 或 'news' (新闻)。
- location: 城市名称 (例如 '北京', '上海')。
- days: 天气预报天数 (1-7天)。
功能特点:
- 天气: 获取当前实时天气及未来最多 7 天的预报信息。
- 新闻: 获取当日的热点新闻摘要。
`
)

type DailyInfoParams struct {
	Type     string `json:"type"`     // "weather" or "news"
	Location string `json:"location"` // Optional, for weather (e.g., "Beijing")
	Days     int    `json:"days"`     // Optional, number of days for weather forecast (1-7)
}

type dailyInfoTool struct {
	client      *http.Client
	permissions permission.Service
}

func NewDailyInfoTool(permissions permission.Service) BaseTool {
	return &dailyInfoTool{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		permissions: permissions,
	}
}

func (t *dailyInfoTool) Info() ToolInfo {
	return ToolInfo{
		Name:        DailyInfoToolName,
		Description: dailyInfoDescription,
		Parameters: map[string]any{
			"type": map[string]any{
				"type":        "string",
				"description": "The type of information to fetch: 'weather' or 'news'",
				"enum":        []string{"weather", "news"},
			},
			"location": map[string]any{
				"type":        "string",
				"description": "The location for weather (e.g., 'Beijing', 'Shanghai'). Defaults to IP-based location if omitted.",
			},
			"days": map[string]any{
				"type":        "integer",
				"description": "The number of days for weather forecast (1-7). Defaults to 1 (today only).",
				"minimum":     1,
				"maximum":     7,
			},
		},
		Required: []string{"type"},
	}
}

func (t *dailyInfoTool) Run(ctx context.Context, params ToolCall) (ToolResponse, error) {
	var args DailyInfoParams
	if err := json.Unmarshal([]byte(params.Input), &args); err != nil {
		return ToolResponse{}, fmt.Errorf("invalid parameters: %w", err)
	}

	// Set default days
	if args.Days <= 0 {
		args.Days = 1
	}
	if args.Days > 7 {
		args.Days = 7
	}

	switch args.Type {
	case "weather":
		return t.getWeather(ctx, args.Location, args.Days)
	case "news":
		return t.getNews(ctx)
	default:
		return ToolResponse{}, fmt.Errorf("unknown type: %s", args.Type)
	}
}

func (t *dailyInfoTool) getWeather(ctx context.Context, location string, days int) (ToolResponse, error) {
	// 0. If location is empty, auto-detect it via IP
	// This is crucial for Open-Meteo fallback, which requires a valid city name.
	if location == "" {
		detected := t.detectLocation(ctx)
		if detected != "" {
			location = detected
			fmt.Printf("Auto-detected location: %s\n", location)
		} else {
			return ToolResponse{}, fmt.Errorf("location is required and auto-detection failed")
		}
	}

	// 1. Primary: Asilu (Baidu wrapper) - Stable & Free
	// This API wraps Baidu Weather and is generally very stable in China
	resp, err := t.getWeatherAsilu(ctx, location, days)
	if err == nil {
		return resp, nil
	}
	fmt.Printf("Asilu failed for %s: %v. Falling back to Open-Meteo\n", location, err)

	// 2. Backup: Open-Meteo (Stable, Global, Free)
	return t.getWeatherOpenMeteo(ctx, location, days)
}

func (t *dailyInfoTool) detectLocation(ctx context.Context) string {
	// Try 1: ip-api.com (Chinese) - Best for China
	// 5s timeout to allow for slower networks
	if loc := t.fetchJSON(ctx, "http://ip-api.com/json/?lang=zh-CN", 5*time.Second); loc != "" {
		return loc
	}

	// Try 2: ipinfo.io (English) - Global Standard
	if loc := t.fetchJSON(ctx, "https://ipinfo.io/json", 5*time.Second); loc != "" {
		return loc
	}

	// Try 3: api.ip.sb (English) - Fast Backup
	if loc := t.fetchJSON(ctx, "https://api.ip.sb/geoip", 5*time.Second); loc != "" {
		return loc
	}

	return ""
}

func (t *dailyInfoTool) fetchJSON(ctx context.Context, url string, timeout time.Duration) string {
	detectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(detectCtx, "GET", url, nil)
	if err != nil {
		return ""
	}
	// Mimic browser to avoid some blocks
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := t.client.Do(req)
	if err != nil {
		fmt.Printf("Location detection failed for %s: %v\n", url, err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Location detection status %d for %s\n", resp.StatusCode, url)
		return ""
	}

	var res struct {
		City string `json:"city"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return ""
	}
	return res.City
}

func (t *dailyInfoTool) getWeatherAsilu(ctx context.Context, location string, days int) (ToolResponse, error) {
	// Removes "市" or "区" suffix for better matching if needed,
	// but Asilu usually handles it well.

	url := fmt.Sprintf("https://query.asilu.com/weather/baidu/?city=%s", location)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ToolResponse{}, err
	}

	// Add User-Agent to avoid some blocks
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := t.client.Do(req)
	if err != nil {
		return ToolResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ToolResponse{}, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	// Response structure based on curl output:
	// {"city":"北京","update_time":"11:30","date":"2月25日","weather":[{"date":"25日（今天）","weather":"多云转小雨","temp":"7/2℃","wind":"西南风转东北风"}...]}
	var res struct {
		City       string `json:"city"`
		UpdateTime string `json:"update_time"`
		Weather    []struct {
			Date    string `json:"date"`
			Weather string `json:"weather"`
			Temp    string `json:"temp"`
			Wind    string `json:"wind"`
		} `json:"weather"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return ToolResponse{}, err
	}

	if len(res.Weather) == 0 {
		return ToolResponse{}, fmt.Errorf("no weather data found")
	}

	// Verify if the returned city matches the requested location
	if !strings.Contains(location, res.City) && !strings.Contains(res.City, location) {
		return ToolResponse{}, fmt.Errorf("city mismatch: requested '%s', got '%s'", location, res.City)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s 天气预报 (Asilu/Baidu):\n", res.City))
	sb.WriteString(fmt.Sprintf("更新时间: %s\n", res.UpdateTime))

	// Limit to requested days
	count := days
	if count > len(res.Weather) {
		count = len(res.Weather)
	}

	for i := 0; i < count; i++ {
		w := res.Weather[i]
		if i == 0 {
			sb.WriteString(fmt.Sprintf("- %s: %s, 温度: %s, 风向: %s\n", w.Date, w.Weather, w.Temp, w.Wind))
		} else {
			sb.WriteString(fmt.Sprintf("- %s: %s, 温度: %s\n", w.Date, w.Weather, w.Temp))
		}
	}

	return ToolResponse{Content: sb.String()}, nil
}

func (t *dailyInfoTool) getWeatherOpenMeteo(ctx context.Context, location string, days int) (ToolResponse, error) {
	// Step 1: Geocoding
	geoUrl := fmt.Sprintf("https://geocoding-api.open-meteo.com/v1/search?name=%s&count=1&language=zh&format=json", location)
	req, err := http.NewRequestWithContext(ctx, "GET", geoUrl, nil)
	if err != nil {
		return ToolResponse{}, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("geocoding failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ToolResponse{}, fmt.Errorf("geocoding status: %d", resp.StatusCode)
	}

	var geoRes struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Name      string  `json:"name"`
			Country   string  `json:"country"`
			Timezone  string  `json:"timezone"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geoRes); err != nil {
		return ToolResponse{}, err
	}
	if len(geoRes.Results) == 0 {
		return ToolResponse{}, fmt.Errorf("location not found")
	}

	place := geoRes.Results[0]

	// Step 2: Weather Forecast
	// We use 'forecast' endpoint with current_weather and daily max/min
	weatherUrl := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%f&longitude=%f&current_weather=true&daily=temperature_2m_max,temperature_2m_min,weathercode&timezone=auto&forecast_days=%d",
		place.Latitude, place.Longitude, days)

	req, err = http.NewRequestWithContext(ctx, "GET", weatherUrl, nil)
	if err != nil {
		return ToolResponse{}, err
	}

	resp, err = t.client.Do(req)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("weather api failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ToolResponse{}, fmt.Errorf("weather api status: %d", resp.StatusCode)
	}

	var wRes struct {
		CurrentWeather struct {
			Temperature float64 `json:"temperature"`
			WindSpeed   float64 `json:"windspeed"`
			WeatherCode int     `json:"weathercode"`
		} `json:"current_weather"`
		Daily struct {
			Time           []string  `json:"time"`
			TemperatureMax []float64 `json:"temperature_2m_max"`
			TemperatureMin []float64 `json:"temperature_2m_min"`
			WeatherCode    []int     `json:"weathercode"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wRes); err != nil {
		return ToolResponse{}, err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s (%s) 天气预报 (Open-Meteo):\n", place.Name, place.Country))
	sb.WriteString(fmt.Sprintf("当前: %.1f°C, 风速: %.1f km/h\n", wRes.CurrentWeather.Temperature, wRes.CurrentWeather.WindSpeed))

	for i := 0; i < len(wRes.Daily.Time); i++ {
		date := wRes.Daily.Time[i]
		high := wRes.Daily.TemperatureMax[i]
		low := wRes.Daily.TemperatureMin[i]
		code := wRes.Daily.WeatherCode[i]
		desc := t.interpretWeatherCode(code)

		sb.WriteString(fmt.Sprintf("- %s: %s, 温度: %.1f°C ~ %.1f°C\n", date, desc, low, high))
	}

	return ToolResponse{Content: sb.String()}, nil
}

func (t *dailyInfoTool) interpretWeatherCode(code int) string {
	switch {
	case code == 0:
		return "晴 (Clear sky)"
	case code >= 1 && code <= 3:
		return "多云 (Partly cloudy)"
	case code >= 45 && code <= 48:
		return "雾 (Fog)"
	case code >= 51 && code <= 55:
		return "毛毛雨 (Drizzle)"
	case code >= 61 && code <= 67:
		return "雨 (Rain)"
	case code >= 71 && code <= 77:
		return "雪 (Snow)"
	case code >= 80 && code <= 82:
		return "阵雨 (Rain showers)"
	case code >= 95 && code <= 99:
		return "雷雨 (Thunderstorm)"
	default:
		return "未知"
	}
}

func (t *dailyInfoTool) getNews(ctx context.Context) (ToolResponse, error) {
	// TopURL News (Fast and stable)
	// We want to fetch both default (Current Affairs/Domestic) and International.
	// API supports 'category' param.
	categories := []string{"", "国际"}
	var allItems []string

	for _, cat := range categories {
		url := "https://news.topurl.cn/api"
		if cat != "" {
			url += "?category=" + cat
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")

		resp, err := t.client.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			continue
		}

		var result struct {
			Code int `json:"code"`
			Data struct {
				NewsList []struct {
					Title    string `json:"title"`
					Category string `json:"category"`
					Url      string `json:"url"`
				} `json:"newsList"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}

		for _, news := range result.Data.NewsList {
			// Format: Title | Category | URL
			// Use a delimiter that is unlikely to appear in titles
			item := fmt.Sprintf("Title: %s | Category: %s | URL: %s", news.Title, news.Category, news.Url)
			allItems = append(allItems, item)
		}
	}

	if len(allItems) > 0 {
		news := "今日新闻列表(Title | Category | URL):\n"
		for i, item := range allItems {
			news += fmt.Sprintf("%d. %s\n", i+1, item)
		}
		return ToolResponse{Content: news}, nil
	}

	// Fallback to backup source if TopURL completely fails
	// ... (Existing fallback logic could go here, but TopURL is very stable. Let's keep it simple for now)

	return ToolResponse{}, fmt.Errorf("failed to fetch news from TopURL")
}
