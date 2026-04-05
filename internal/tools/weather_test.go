package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWeatherToolInterface(t *testing.T) {
	tool := NewWeatherTool(WeatherConfig{})
	assert.Equal(t, "weather", tool.Name())
	assert.NotEmpty(t, tool.Description())

	params := tool.Parameters()
	assert.Equal(t, "object", params["type"])

	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "location")
	assert.Contains(t, props, "days")
	assert.Contains(t, props, "units")
}

func TestWmoDescription(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "Ясно"},
		{1, "Преимущественно ясно"},
		{2, "Переменная облачность"},
		{3, "Пасмурно"},
		{45, "Туман"},
		{51, "Лёгкая морось"},
		{61, "Небольшой дождь"},
		{63, "Дождь"},
		{65, "Сильный дождь"},
		{71, "Небольшой снег"},
		{80, "Небольшой ливень"},
		{95, "Гроза"},
		{96, "Гроза с градом"},
		{99, "Сильная гроза с градом"},
		{999, "Переменная погода"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, wmoDescription(tc.code), "code=%d", tc.code)
	}
}

func TestWmoEmoji(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "☀️"},
		{1, "🌤"},
		{2, "⛅"},
		{3, "☁️"},
		{45, "🌫"},
		{53, "🌦"},
		{61, "🌧"},
		{71, "❄️"},
		{80, "🌧"},
		{85, "🌨"},
		{95, "⛈"},
		{999, "🌡"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, wmoEmoji(tc.code), "code=%d", tc.code)
	}
}

func TestWindDirection(t *testing.T) {
	tests := []struct {
		deg  int
		want string
	}{
		{0, "С"},
		{45, "СВ"},
		{90, "В"},
		{135, "ЮВ"},
		{180, "Ю"},
		{225, "ЮЗ"},
		{270, "З"},
		{315, "СЗ"},
		{360, "С"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, windDirection(tc.deg), "deg=%d", tc.deg)
	}
}

func TestRussianWeekday(t *testing.T) {
	assert.Equal(t, "Пн", russianWeekday(time.Monday))
	assert.Equal(t, "Вт", russianWeekday(time.Tuesday))
	assert.Equal(t, "Ср", russianWeekday(time.Wednesday))
	assert.Equal(t, "Чт", russianWeekday(time.Thursday))
	assert.Equal(t, "Пт", russianWeekday(time.Friday))
	assert.Equal(t, "Сб", russianWeekday(time.Saturday))
	assert.Equal(t, "Вс", russianWeekday(time.Sunday))
}

func TestExtractTime(t *testing.T) {
	assert.Equal(t, "07:23", extractTime("2024-01-15T07:23"))
	assert.Equal(t, "07:23", extractTime("2024-01-15T07:23+03:00"))
	assert.Equal(t, "19:45", extractTime("2024-06-21T19:45"))
	assert.Equal(t, "short", extractTime("short")) // no 'T' → return as-is
}

func TestWeatherSliceHelpers(t *testing.T) {
	floats := []float64{1.5, 2.5, 3.5}
	assert.Equal(t, 1.5, weatherSliceFloat(floats, 0))
	assert.Equal(t, 2.5, weatherSliceFloat(floats, 1))
	assert.Equal(t, 0.0, weatherSliceFloat(floats, 10))
	assert.Equal(t, 0.0, weatherSliceFloat(nil, 0))

	ints := []int{10, 20, 30}
	assert.Equal(t, 10, weatherSliceInt(ints, 0))
	assert.Equal(t, 20, weatherSliceInt(ints, 1))
	assert.Equal(t, 0, weatherSliceInt(ints, 5))

	strs := []string{"a", "b"}
	assert.Equal(t, "a", weatherSliceStr(strs, 0))
	assert.Equal(t, "", weatherSliceStr(strs, 5))
}

func TestFormatWeatherOutput(t *testing.T) {
	geo := &geoResult{
		Name:      "Москва",
		Admin1:    "Москва",
		Country:   "Россия",
		Latitude:  55.7558,
		Longitude: 37.6173,
		Timezone:  "Europe/Moscow",
	}

	f := &forecastResponse{
		Timezone: "Europe/Moscow",
		Current: currentWeather{
			Time:                "2024-06-15T14:00",
			Temperature2m:       22.5,
			RelativeHumidity2m:  58,
			ApparentTemperature: 21.0,
			Precipitation:       0,
			WeatherCode:         1,
			WindSpeed10m:        12.3,
			WindDirection10m:    270,
			SurfacePressure:     1013.0,
			Visibility:          10000,
		},
		Daily: dailyForecast{
			Time:                        []string{"2024-06-15", "2024-06-16"},
			WeatherCode:                 []int{1, 63},
			Temperature2mMax:            []float64{24.0, 18.0},
			Temperature2mMin:            []float64{14.0, 12.0},
			PrecipitationSum:            []float64{0.0, 5.2},
			PrecipitationProbabilityMax: []int{5, 80},
			WindSpeed10mMax:             []float64{15.0, 22.0},
			WindDirection10mDominant:    []int{270, 180},
			UvIndexMax:                  []float64{6.0, 2.0},
			Sunrise:                     []string{"2024-06-15T04:47", "2024-06-16T04:48"},
			Sunset:                      []string{"2024-06-15T21:22", "2024-06-16T21:21"},
		},
	}

	result := formatWeather(geo, f, 2, "celsius")

	assert.Contains(t, result, "📍 Москва, Москва, Россия")
	assert.Contains(t, result, "22.5°C")
	assert.Contains(t, result, "Влажность: 58%")
	assert.Contains(t, result, "Давление: 1013 гПа")
	assert.Contains(t, result, "Прогноз на 2 дн.")
	assert.Contains(t, result, "Преимущественно ясно")
	assert.Contains(t, result, "Дождь")
	assert.Contains(t, result, "04:47")
	assert.Contains(t, result, "21:22")
}
