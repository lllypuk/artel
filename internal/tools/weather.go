package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WeatherTool fetches detailed weather forecast using Open-Meteo API (free, no API key needed).
type WeatherTool struct {
	client *http.Client
}

// WeatherConfig holds configuration for the weather tool.
type WeatherConfig struct {
	TimeoutSeconds int
	Proxy          string
}

// NewWeatherTool creates a new weather tool.
func NewWeatherTool(cfg WeatherConfig) *WeatherTool {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	transport := &http.Transport{}
	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	return &WeatherTool{
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

type weatherArgs struct {
	Location string `json:"location"`
	Days     int    `json:"days"`  // 1–7, default 3
	Units    string `json:"units"` // celsius | fahrenheit
}

func (w *WeatherTool) Name() string { return "weather" }

func (w *WeatherTool) Description() string {
	return "Get detailed weather forecast for any location using Open-Meteo API (free, no API key required). " +
		"Returns current conditions (temperature, humidity, wind, pressure, visibility) " +
		"and a multi-day daily forecast with min/max temperatures, precipitation probability, " +
		"wind speed, UV index, sunrise and sunset times."
}

func (w *WeatherTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"location": map[string]any{
				"type":        "string",
				"description": "City name or location (e.g. 'Moscow', 'London', 'New York')",
			},
			"days": map[string]any{
				"type":        "integer",
				"description": "Number of forecast days (1–7, default: 3)",
				"minimum":     1,
				"maximum":     7,
			},
			"units": map[string]any{
				"type":        "string",
				"enum":        []string{"celsius", "fahrenheit"},
				"description": "Temperature units (default: celsius)",
			},
		},
		"required": []string{"location"},
	}
}

// ─── Geocoding API types ──────────────────────────────────────────────────────

type geoResponse struct {
	Results []geoResult `json:"results"`
}

type geoResult struct {
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Country   string  `json:"country"`
	Admin1    string  `json:"admin1"`
	Timezone  string  `json:"timezone"`
}

// ─── Forecast API types ───────────────────────────────────────────────────────

type forecastResponse struct {
	Timezone string         `json:"timezone"`
	Current  currentWeather `json:"current"`
	Daily    dailyForecast  `json:"daily"`
}

type currentWeather struct {
	Time                string  `json:"time"`
	Temperature2m       float64 `json:"temperature_2m"`
	RelativeHumidity2m  int     `json:"relative_humidity_2m"`
	ApparentTemperature float64 `json:"apparent_temperature"`
	Precipitation       float64 `json:"precipitation"`
	WeatherCode         int     `json:"weather_code"`
	WindSpeed10m        float64 `json:"wind_speed_10m"`
	WindDirection10m    int     `json:"wind_direction_10m"`
	SurfacePressure     float64 `json:"surface_pressure"`
	Visibility          float64 `json:"visibility"`
}

type dailyForecast struct {
	Time                        []string  `json:"time"`
	WeatherCode                 []int     `json:"weather_code"`
	Temperature2mMax            []float64 `json:"temperature_2m_max"`
	Temperature2mMin            []float64 `json:"temperature_2m_min"`
	PrecipitationSum            []float64 `json:"precipitation_sum"`
	PrecipitationProbabilityMax []int     `json:"precipitation_probability_max"`
	WindSpeed10mMax             []float64 `json:"wind_speed_10m_max"`
	WindDirection10mDominant    []int     `json:"wind_direction_10m_dominant"`
	UvIndexMax                  []float64 `json:"uv_index_max"`
	Sunrise                     []string  `json:"sunrise"`
	Sunset                      []string  `json:"sunset"`
}

// ─── Execute ──────────────────────────────────────────────────────────────────

func (w *WeatherTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a weatherArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.Location == "" {
		return "", fmt.Errorf("location is required")
	}
	if a.Days <= 0 {
		a.Days = 3
	}
	if a.Days > 7 {
		a.Days = 7
	}
	if a.Units == "" {
		a.Units = "celsius"
	}

	geo, err := w.geocode(ctx, a.Location)
	if err != nil {
		return "", fmt.Errorf("geocoding %q: %w", a.Location, err)
	}

	forecast, err := w.fetchForecast(ctx, geo, a.Days, a.Units)
	if err != nil {
		return "", fmt.Errorf("fetch forecast: %w", err)
	}

	return formatWeather(geo, forecast, a.Days, a.Units), nil
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (w *WeatherTool) geocode(ctx context.Context, location string) (*geoResult, error) {
	reqURL := "https://geocoding-api.open-meteo.com/v1/search?" +
		"name=" + url.QueryEscape(location) +
		"&count=1&language=ru&format=json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("geocoding API %d: %s", resp.StatusCode, string(body))
	}

	var data geoResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode geocoding: %w", err)
	}
	if len(data.Results) == 0 {
		return nil, fmt.Errorf("location not found: %q", location)
	}
	return &data.Results[0], nil
}

func (w *WeatherTool) fetchForecast(ctx context.Context, geo *geoResult, days int, units string) (*forecastResponse, error) {
	tempUnit := "celsius"
	if units == "fahrenheit" {
		tempUnit = "fahrenheit"
	}

	params := url.Values{}
	params.Set("latitude", fmt.Sprintf("%.4f", geo.Latitude))
	params.Set("longitude", fmt.Sprintf("%.4f", geo.Longitude))
	params.Set("timezone", "auto")
	params.Set("forecast_days", fmt.Sprintf("%d", days))
	params.Set("temperature_unit", tempUnit)
	params.Set("current", strings.Join([]string{
		"temperature_2m",
		"relative_humidity_2m",
		"apparent_temperature",
		"precipitation",
		"weather_code",
		"wind_speed_10m",
		"wind_direction_10m",
		"surface_pressure",
		"visibility",
	}, ","))
	params.Set("daily", strings.Join([]string{
		"weather_code",
		"temperature_2m_max",
		"temperature_2m_min",
		"precipitation_sum",
		"precipitation_probability_max",
		"wind_speed_10m_max",
		"wind_direction_10m_dominant",
		"uv_index_max",
		"sunrise",
		"sunset",
	}, ","))

	reqURL := "https://api.open-meteo.com/v1/forecast?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("forecast API %d: %s", resp.StatusCode, string(body))
	}

	var data forecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode forecast: %w", err)
	}
	return &data, nil
}

// ─── Formatting ───────────────────────────────────────────────────────────────

func formatWeather(geo *geoResult, f *forecastResponse, days int, units string) string {
	var sb strings.Builder

	tempSym := "°C"
	if units == "fahrenheit" {
		tempSym = "°F"
	}

	// Location header
	loc := geo.Name
	if geo.Admin1 != "" {
		loc += ", " + geo.Admin1
	}
	if geo.Country != "" {
		loc += ", " + geo.Country
	}
	fmt.Fprintf(&sb, "📍 %s\n", loc)
	fmt.Fprintf(&sb, "🕐 %s (%s)\n\n", f.Current.Time, f.Timezone)

	// Current conditions
	c := f.Current
	fmt.Fprintf(&sb, "━━━ Сейчас ━━━\n")
	fmt.Fprintf(&sb, "%s %s\n", wmoEmoji(c.WeatherCode), wmoDescription(c.WeatherCode))
	fmt.Fprintf(&sb, "🌡 %.1f%s (ощущается %.1f%s)\n",
		c.Temperature2m, tempSym, c.ApparentTemperature, tempSym)
	fmt.Fprintf(&sb, "💧 Влажность: %d%%\n", c.RelativeHumidity2m)
	fmt.Fprintf(&sb, "💨 Ветер: %.1f км/ч %s\n", c.WindSpeed10m, windDirection(c.WindDirection10m))
	fmt.Fprintf(&sb, "🔵 Давление: %.0f гПа\n", c.SurfacePressure)
	if c.Visibility > 0 {
		fmt.Fprintf(&sb, "👁 Видимость: %.0f м\n", c.Visibility)
	}
	if c.Precipitation > 0 {
		fmt.Fprintf(&sb, "🌧 Осадки (1ч): %.1f мм\n", c.Precipitation)
	}

	// Daily forecast
	d := f.Daily
	if len(d.Time) == 0 {
		return sb.String()
	}

	fmt.Fprintf(&sb, "\n━━━ Прогноз на %d дн. ━━━\n", days)
	for i := 0; i < len(d.Time) && i < days; i++ {
		dateStr := d.Time[i]
		date, err := time.Parse("2006-01-02", dateStr)
		dayLabel := dateStr
		if err == nil {
			dayLabel = date.Format("02.01") + " " + russianWeekday(date.Weekday())
		}

		wcode := weatherSliceInt(d.WeatherCode, i)
		tmax := weatherSliceFloat(d.Temperature2mMax, i)
		tmin := weatherSliceFloat(d.Temperature2mMin, i)
		precip := weatherSliceFloat(d.PrecipitationSum, i)
		precipProb := weatherSliceInt(d.PrecipitationProbabilityMax, i)
		windMax := weatherSliceFloat(d.WindSpeed10mMax, i)
		windDom := weatherSliceInt(d.WindDirection10mDominant, i)
		uvIdx := weatherSliceFloat(d.UvIndexMax, i)
		sunrise := weatherSliceStr(d.Sunrise, i)
		sunset := weatherSliceStr(d.Sunset, i)

		fmt.Fprintf(&sb, "\n%s %s — %s\n", wmoEmoji(wcode), dayLabel, wmoDescription(wcode))
		fmt.Fprintf(&sb, "  🌡 %.0f%s / %.0f%s", tmin, tempSym, tmax, tempSym)
		if precipProb > 0 || precip > 0 {
			fmt.Fprintf(&sb, "  |  🌧 %.1f мм (%d%%)", precip, precipProb)
		}
		fmt.Fprintln(&sb)

		line2 := fmt.Sprintf("  💨 до %.0f км/ч %s", windMax, windDirection(windDom))
		if uvIdx > 0 {
			line2 += fmt.Sprintf("  |  ☀️ УФ-индекс: %.0f", uvIdx)
		}
		fmt.Fprintln(&sb, line2)

		if sunrise != "" && sunset != "" {
			fmt.Fprintf(&sb, "  🌅 %s  🌇 %s\n",
				extractTime(sunrise), extractTime(sunset))
		}
	}

	return sb.String()
}

// ─── Helper functions ─────────────────────────────────────────────────────────

func weatherSliceFloat(s []float64, i int) float64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func weatherSliceInt(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func weatherSliceStr(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

// extractTime returns "HH:MM" from ISO datetime strings like "2024-01-15T07:23" or "2024-01-15T07:23+03:00".
func extractTime(s string) string {
	// Find the 'T' separator
	idx := strings.IndexByte(s, 'T')
	if idx < 0 || idx+6 > len(s) {
		return s
	}
	return s[idx+1 : idx+6]
}

// russianWeekday returns the Russian abbreviated weekday name.
func russianWeekday(d time.Weekday) string {
	names := [7]string{"Вс", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб"}
	return names[d]
}

// windDirection converts a bearing in degrees to an 8-point Russian compass direction.
func windDirection(deg int) string {
	dirs := [8]string{"С", "СВ", "В", "ЮВ", "Ю", "ЮЗ", "З", "СЗ"}
	idx := ((deg + 22) % 360) / 45
	if idx < 0 || idx >= len(dirs) {
		return ""
	}
	return dirs[idx]
}

// wmoDescriptions maps WMO weather interpretation codes to Russian descriptions.
//
//nolint:gochecknoglobals
var wmoDescriptions = map[int]string{
	0:  "Ясно",
	1:  "Преимущественно ясно",
	2:  "Переменная облачность",
	3:  "Пасмурно",
	45: "Туман",
	48: "Изморозевый туман",
	51: "Лёгкая морось",
	53: "Морось",
	55: "Плотная морось",
	56: "Лёгкий ледяной дождь",
	57: "Ледяной дождь",
	61: "Небольшой дождь",
	63: "Дождь",
	65: "Сильный дождь",
	66: "Лёгкий ледяной ливень",
	67: "Ледяной ливень",
	71: "Небольшой снег",
	73: "Снег",
	75: "Сильный снег",
	77: "Снежная крупа",
	80: "Небольшой ливень",
	81: "Ливень",
	82: "Сильный ливень",
	85: "Снегопад",
	86: "Сильный снегопад",
	95: "Гроза",
	96: "Гроза с градом",
	99: "Сильная гроза с градом",
}

// wmoDescription maps a WMO weather interpretation code to a Russian description.
func wmoDescription(code int) string {
	if desc, ok := wmoDescriptions[code]; ok {
		return desc
	}
	return "Переменная погода"
}

// wmoEmoji maps a WMO weather code to an emoji.
func wmoEmoji(code int) string {
	switch {
	case code == 0:
		return "☀️"
	case code == 1:
		return "🌤"
	case code == 2:
		return "⛅"
	case code == 3:
		return "☁️"
	case code == 45 || code == 48:
		return "🌫"
	case code >= 51 && code <= 57:
		return "🌦"
	case code >= 61 && code <= 67:
		return "🌧"
	case code >= 71 && code <= 77:
		return "❄️"
	case code >= 80 && code <= 82:
		return "🌧"
	case code >= 85 && code <= 86:
		return "🌨"
	case code >= 95 && code <= 99:
		return "⛈"
	default:
		return "🌡"
	}
}
