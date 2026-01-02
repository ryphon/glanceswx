package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/joho/godotenv"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginBottom(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	valueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD")).
			Padding(1, 2)
)


type WeatherData struct {
	Temperature    float64
	Humidity       float64
	Pressure       float64
	WindSpeed      float64
	WindDirection  float64
	WindGust24h    float64
	WindGustMax    float64
	UV             float64
	SolarRadiation float64
	Precipitation  float64
	BatteryVoltage float64
	StationUptime  int64
	UpdatedAt      time.Time
}

type model struct {
	weather            WeatherData
	spinner            spinner.Model
	loading            bool
	err                error
	client             influxdb2.Client
	bucket             string
	org                string
	weatherMeasurement string
	statusMeasurement  string
	width              int
	height             int
}

type tickMsg time.Time
type weatherMsg WeatherData
type errMsg error

func initialModel() model {
	_ = godotenv.Load()

	host := os.Getenv("INFLUX_HOST")
	token := os.Getenv("INFLUX_TOKEN")
	org := os.Getenv("INFLUX_ORG")
	bucket := os.Getenv("INFLUX_BUCKET")
	weatherMeasurement := os.Getenv("WEATHER_MEASUREMENT")
	statusMeasurement := os.Getenv("STATUS_MEASUREMENT")

	if host == "" || token == "" || org == "" || bucket == "" {
		panic("Missing required InfluxDB environment variables")
	}

	if weatherMeasurement == "" || statusMeasurement == "" {
		panic("Missing required measurement environment variables (WEATHER_MEASUREMENT, STATUS_MEASUREMENT)")
	}

	client := influxdb2.NewClient(host, token)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))

	return model{
		spinner:            s,
		loading:            true,
		client:             client,
		bucket:             bucket,
		org:                org,
		weatherMeasurement: weatherMeasurement,
		statusMeasurement:  statusMeasurement,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchWeather(m.client, m.bucket, m.org, m.weatherMeasurement, m.statusMeasurement),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchWeather(client influxdb2.Client, bucket, org, weatherMeasurement, statusMeasurement string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		queryAPI := client.QueryAPI(org)

		weather := WeatherData{UpdatedAt: time.Now()}

		// Current weather data
		query := fmt.Sprintf(`
from(bucket: "%s")
  |> range(start: -5m)
  |> filter(fn: (r) => r._measurement == "%s")
  |> last()
`, bucket, weatherMeasurement)

		result, err := queryAPI.Query(ctx, query)
		if err != nil {
			return errMsg(err)
		}

		for result.Next() {
			record := result.Record()
			field := record.Field()
			value := record.Value()

			switch field {
			case "temperature_air":
				if v, ok := value.(float64); ok {
					weather.Temperature = v*9.0/5.0 + 32.0
				}
			case "r-humidity":
				if v, ok := value.(float64); ok {
					weather.Humidity = v
				}
			case "pressure_station":
				if v, ok := value.(float64); ok {
					weather.Pressure = v / 100.0
				}
			case "wind_speed":
				if v, ok := value.(float64); ok {
					weather.WindSpeed = v * 2.237
				}
			case "wind_direction":
				if v, ok := value.(float64); ok {
					weather.WindDirection = v
				}
			case "uv":
				if v, ok := value.(float64); ok {
					weather.UV = v
				}
			case "irradiance_sun":
				if v, ok := value.(float64); ok {
					weather.SolarRadiation = v
				}
			case "rain_accumulation":
				if v, ok := value.(float64); ok {
					weather.Precipitation = v / 25.4
				}
			}
		}

		if result.Err() != nil {
			return errMsg(result.Err())
		}

		// Wind gust 24h max
		query = fmt.Sprintf(`
from(bucket: "%s")
  |> range(start: -24h)
  |> filter(fn: (r) => r._measurement == "%s")
  |> filter(fn: (r) => r._field == "wind_gust")
  |> max()
`, bucket, weatherMeasurement)

		result, err = queryAPI.Query(ctx, query)
		if err == nil {
			for result.Next() {
				if v, ok := result.Record().Value().(float64); ok {
					weather.WindGust24h = v * 2.237
				}
			}
		}

		// Wind gust all time max
		query = fmt.Sprintf(`
from(bucket: "%s")
  |> range(start: -365d)
  |> filter(fn: (r) => r._measurement == "%s")
  |> filter(fn: (r) => r._field == "wind_gust")
  |> max()
`, bucket, weatherMeasurement)

		result, err = queryAPI.Query(ctx, query)
		if err == nil {
			for result.Next() {
				if v, ok := result.Record().Value().(float64); ok {
					weather.WindGustMax = v * 2.237
				}
			}
		}

		// Station status (battery and uptime)
		query = fmt.Sprintf(`
from(bucket: "%s")
  |> range(start: -5m)
  |> filter(fn: (r) => r._measurement == "%s")
  |> last()
`, bucket, statusMeasurement)

		result, err = queryAPI.Query(ctx, query)
		if err == nil {
			for result.Next() {
				record := result.Record()
				field := record.Field()
				value := record.Value()

				switch field {
				case "voltage":
					if v, ok := value.(float64); ok {
						weather.BatteryVoltage = v
					}
				case "uptime":
					if v, ok := value.(int64); ok {
						weather.StationUptime = v
					} else if v, ok := value.(float64); ok {
						weather.StationUptime = int64(v)
					}
				}
			}
		}

		return weatherMsg(weather)
	}
}


func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.client.Close()
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case weatherMsg:
		m.weather = WeatherData(msg)
		m.loading = false
		return m, nil

	case tickMsg:
		return m, tea.Batch(
			fetchWeather(m.client, m.bucket, m.org, m.weatherMeasurement, m.statusMeasurement),
			tickCmd(),
		)

	case errMsg:
		m.err = error(msg)
		m.loading = false
		return m, nil
	}

	return m, nil
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	if m.loading {
		return fmt.Sprintf("\n\n   %s Loading weather data...\n\n", m.spinner.View())
	}

	var b strings.Builder

	// Title
	title := titleStyle.Render("🌤  LCAR.earth Weather Station")
	b.WriteString(title + "\n")

	// Current conditions
	currentBox := m.renderCurrent()

	// Layout
	b.WriteString(currentBox)

	// Footer
	footer := labelStyle.Render(fmt.Sprintf("\nUpdated: %s | Press q to quit",
		m.weather.UpdatedAt.Format("15:04:05")))
	b.WriteString("\n" + footer)

	return b.String()
}

func (m model) renderCurrent() string {
	// Left column: main weather data
	var leftRows []string

	// Temperature
	temp := fmt.Sprintf("%s %.1f°F", labelStyle.Render("Temperature:    "), m.weather.Temperature)
	leftRows = append(leftRows, valueStyle.Render(temp))

	// Humidity
	humidity := fmt.Sprintf("%s %.0f%%", labelStyle.Render("Humidity:       "), m.weather.Humidity)
	leftRows = append(leftRows, valueStyle.Render(humidity))

	// Pressure
	pressure := fmt.Sprintf("%s %.1f mb", labelStyle.Render("Pressure:       "), m.weather.Pressure)
	leftRows = append(leftRows, valueStyle.Render(pressure))

	// Wind speed
	wind := fmt.Sprintf("%s %.1f mph", labelStyle.Render("Wind Speed:     "), m.weather.WindSpeed)
	leftRows = append(leftRows, valueStyle.Render(wind))

	// Wind Gust 24h
	gust24h := fmt.Sprintf("%s %.1f mph", labelStyle.Render("Gust (24h):     "), m.weather.WindGust24h)
	leftRows = append(leftRows, valueStyle.Render(gust24h))

	// Wind Gust Max
	gustMax := fmt.Sprintf("%s %.1f mph", labelStyle.Render("Gust (max):     "), m.weather.WindGustMax)
	leftRows = append(leftRows, valueStyle.Render(gustMax))

	// UV Index
	uv := fmt.Sprintf("%s %.1f", labelStyle.Render("UV Index:       "), m.weather.UV)
	leftRows = append(leftRows, valueStyle.Render(uv))

	// Solar Radiation
	solar := fmt.Sprintf("%s %.0f W/m²", labelStyle.Render("Solar:          "), m.weather.SolarRadiation)
	leftRows = append(leftRows, valueStyle.Render(solar))

	// Precipitation
	precip := fmt.Sprintf("%s %.2f in", labelStyle.Render("Rain (24h):     "), m.weather.Precipitation)
	leftRows = append(leftRows, valueStyle.Render(precip))

	// Battery
	battery := fmt.Sprintf("%s %.2fV", labelStyle.Render("Battery:        "), m.weather.BatteryVoltage)
	leftRows = append(leftRows, valueStyle.Render(battery))

	// Uptime
	uptimeStr := formatUptime(m.weather.StationUptime)
	uptime := fmt.Sprintf("%s %s", labelStyle.Render("Station Uptime: "), uptimeStr)
	leftRows = append(leftRows, valueStyle.Render(uptime))

	leftColumn := strings.Join(leftRows, "\n")

	// Right column: compass
	compassBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#874BFD")).
		Padding(1, 2).
		Render(renderCompass(m.weather.WindDirection))

	// Join columns side by side
	content := lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, "  ", compassBox)

	return boxStyle.Render(content)
}

func formatUptime(seconds int64) string {
	if seconds == 0 {
		return "N/A"
	}

	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func getWindDirection(degrees float64) string {
	directions := []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	index := int(math.Round(degrees/22.5)) % 16
	return directions[index]
}

func renderCompass(degrees float64) string {
	// Determine which direction to highlight
	dir := getWindDirection(degrees)

	// Unicode arrows for each direction
	arrows := map[string]string{
		"N":   "↑", "NNE": "↑", "NE": "↗", "ENE": "→",
		"E":   "→", "ESE": "→", "SE": "↘", "SSE": "↓",
		"S":   "↓", "SSW": "↓", "SW": "↙", "WSW": "←",
		"W":   "←", "WNW": "←", "NW": "↖", "NNW": "↑",
	}

	arrow := arrows[dir]

	highlightStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF6B6B"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#555555"))

	arrowStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF6B6B"))

	// Helper function to highlight current direction
	highlight := func(d string) string {
		if d == dir {
			return highlightStyle.Render(d)
		}
		return dimStyle.Render(d)
	}

	// Build compass - more spaced out
	line1 := fmt.Sprintf("          %s", highlight("N"))
	line2 := fmt.Sprintf("   %s            %s", highlight("NW"), highlight("NE"))
	line3 := fmt.Sprintf("")
	line4 := fmt.Sprintf(" %s        %s        %s", highlight("W"), arrowStyle.Render(arrow), highlight("E"))
	line5 := fmt.Sprintf("")
	line6 := fmt.Sprintf("   %s            %s", highlight("SW"), highlight("SE"))
	line7 := fmt.Sprintf("          %s", highlight("S"))

	return strings.Join([]string{line1, line2, line3, line4, line5, line6, line7}, "\n")
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
