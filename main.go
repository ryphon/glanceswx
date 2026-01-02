package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"sync"
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

type DataSource int

const (
	SourceUDP DataSource = iota
	SourceInfluxDB
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

// TempestObservation represents the UDP broadcast message from Tempest
type TempestObservation struct {
	SerialNumber string      `json:"serial_number"`
	Type         string      `json:"type"`
	HubSN        string      `json:"hub_sn"`
	Obs          [][]float64 `json:"obs"`
}

// TempestStatus represents device status messages
type TempestStatus struct {
	SerialNumber string  `json:"serial_number"`
	Type         string  `json:"type"`
	HubSN        string  `json:"hub_sn"`
	Timestamp    int64   `json:"timestamp"`
	Uptime       int64   `json:"uptime"`
	Voltage      float64 `json:"voltage"`
}

type model struct {
	weather            WeatherData
	spinner            spinner.Model
	loading            bool
	err                error
	width              int
	height             int
	dataSource         DataSource
	udpListener        *UDPListener
	client             influxdb2.Client
	bucket             string
	org                string
	weatherMeasurement string
	statusMeasurement  string
}

type tickMsg time.Time
type weatherMsg WeatherData
type errMsg error

// UDPListener handles receiving Tempest weather data
type UDPListener struct {
	conn        *net.UDPConn
	weatherData WeatherData
	mu          sync.RWMutex
	windGusts   []windGustRecord
	program     *tea.Program
}

type windGustRecord struct {
	gust      float64
	timestamp time.Time
}

func NewUDPListener() (*UDPListener, error) {
	addr := net.UDPAddr{
		Port: 50222,
		IP:   net.IPv4zero,
	}

	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on UDP port 50222: %w", err)
	}

	return &UDPListener{
		conn:        conn,
		weatherData: WeatherData{UpdatedAt: time.Now()},
		windGusts:   make([]windGustRecord, 0),
	}, nil
}

func (u *UDPListener) Start(p *tea.Program) {
	u.program = p
	go u.listen()
	go u.cleanupOldGusts()
}

func (u *UDPListener) listen() {
	buffer := make([]byte, 4096)

	for {
		n, _, err := u.conn.ReadFromUDP(buffer)
		if err != nil {
			continue
		}

		u.handleMessage(buffer[:n])
	}
}

func (u *UDPListener) handleMessage(data []byte) {
	// Try to parse as observation
	var obs TempestObservation
	if err := json.Unmarshal(data, &obs); err == nil && obs.Type == "obs_st" {
		u.processObservation(obs)
		return
	}

	// Try to parse as status
	var status TempestStatus
	if err := json.Unmarshal(data, &status); err == nil && status.Type == "device_status" {
		u.processStatus(status)
		return
	}
}

func (u *UDPListener) processObservation(obs TempestObservation) {
	if len(obs.Obs) == 0 || len(obs.Obs[0]) < 18 {
		return
	}

	// obs_st format:
	// [0] Time Epoch
	// [1] Wind Lull (m/s)
	// [2] Wind Avg (m/s)
	// [3] Wind Gust (m/s)
	// [4] Wind Direction (degrees)
	// [5] Wind Sample Interval (seconds)
	// [6] Station Pressure (MB)
	// [7] Air Temperature (C)
	// [8] Relative Humidity (%)
	// [9] Illuminance (lux)
	// [10] UV Index
	// [11] Solar Radiation (W/m^2)
	// [12] Rain Accumulated (mm)
	// [13] Precipitation Type
	// [14] Lightning Strike Avg Distance (km)
	// [15] Lightning Strike Count
	// [16] Battery (volts)
	// [17] Report Interval (minutes)

	data := obs.Obs[0]

	u.mu.Lock()
	defer u.mu.Unlock()

	u.weatherData.WindSpeed = data[2] * 2.237          // m/s to mph
	u.weatherData.WindDirection = data[4]              // degrees
	u.weatherData.Pressure = data[6]                   // already in mb
	u.weatherData.Temperature = data[7]*9.0/5.0 + 32.0 // C to F
	u.weatherData.Humidity = data[8]                   // %
	u.weatherData.UV = data[10]                        // UV index
	u.weatherData.SolarRadiation = data[11]            // W/m^2
	u.weatherData.Precipitation = data[12] / 25.4      // mm to inches
	u.weatherData.BatteryVoltage = data[16]            // volts
	u.weatherData.UpdatedAt = time.Now()

	// Track wind gust
	gust := data[3] * 2.237 // m/s to mph
	u.windGusts = append(u.windGusts, windGustRecord{
		gust:      gust,
		timestamp: time.Now(),
	})

	// Calculate max gusts
	u.calculateWindGusts()

	// Send update to program
	if u.program != nil {
		u.program.Send(weatherMsg(u.weatherData))
	}
}

func (u *UDPListener) processStatus(status TempestStatus) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.weatherData.StationUptime = status.Uptime
	u.weatherData.BatteryVoltage = status.Voltage

	// Send update to program
	if u.program != nil {
		u.program.Send(weatherMsg(u.weatherData))
	}
}

func (u *UDPListener) calculateWindGusts() {
	now := time.Now()
	max24h := 0.0
	maxAllTime := 0.0

	for _, record := range u.windGusts {
		if record.gust > maxAllTime {
			maxAllTime = record.gust
		}
		if now.Sub(record.timestamp) <= 24*time.Hour && record.gust > max24h {
			max24h = record.gust
		}
	}

	u.weatherData.WindGust24h = max24h
	u.weatherData.WindGustMax = maxAllTime
}

func (u *UDPListener) cleanupOldGusts() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		u.mu.Lock()
		now := time.Now()
		filtered := make([]windGustRecord, 0)
		for _, record := range u.windGusts {
			// Keep records from last 365 days
			if now.Sub(record.timestamp) <= 365*24*time.Hour {
				filtered = append(filtered, record)
			}
		}
		u.windGusts = filtered
		u.mu.Unlock()
	}
}

func (u *UDPListener) Close() {
	if u.conn != nil {
		u.conn.Close()
	}
}

func fetchWeatherInfluxDB(client influxdb2.Client, bucket, org, weatherMeasurement, statusMeasurement string) tea.Cmd {
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

func getConfig(mode, host, token, org, bucket, weatherMeasurement, statusMeasurement *string) (string, string, string, string, string, string) {
	// Helper to get value from CLI arg or env var
	getValue := func(cliValue *string, envKey string) string {
		if cliValue != nil && *cliValue != "" {
			return *cliValue
		}
		return os.Getenv(envKey)
	}

	return getValue(host, "INFLUX_HOST"),
		getValue(token, "INFLUX_TOKEN"),
		getValue(org, "INFLUX_ORG"),
		getValue(bucket, "INFLUX_BUCKET"),
		getValue(weatherMeasurement, "WEATHER_MEASUREMENT"),
		getValue(statusMeasurement, "STATUS_MEASUREMENT")
}

func initialModel() model {
	_ = godotenv.Load()

	// Parse CLI flags
	mode := flag.String("mode", "", "Data source mode: 'udp' or 'influxdb' (default: auto-detect)")
	influxHost := flag.String("influx-host", "", "InfluxDB host URL")
	influxToken := flag.String("influx-token", "", "InfluxDB token")
	influxOrg := flag.String("influx-org", "", "InfluxDB organization")
	influxBucket := flag.String("influx-bucket", "", "InfluxDB bucket")
	weatherMeasurement := flag.String("weather-measurement", "", "Weather observation measurement name")
	statusMeasurement := flag.String("status-measurement", "", "Status measurement name")
	flag.Parse()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))

	// Get configuration from CLI args or env vars
	host, token, org, bucket, weatherMeas, statusMeas := getConfig(
		mode, influxHost, influxToken, influxOrg, influxBucket, weatherMeasurement, statusMeasurement,
	)

	// Determine data source
	useInfluxDB := false
	if mode != nil && *mode == "influxdb" {
		useInfluxDB = true
	} else if mode != nil && *mode == "udp" {
		useInfluxDB = false
	} else {
		// Auto-detect: use InfluxDB if all required config is present
		hasInfluxConfig := host != "" && token != "" && org != "" && bucket != "" &&
			weatherMeas != "" && statusMeas != ""
		useInfluxDB = hasInfluxConfig
	}

	if useInfluxDB {
		// Validate InfluxDB configuration
		if host == "" || token == "" || org == "" || bucket == "" || weatherMeas == "" || statusMeas == "" {
			return model{
				spinner:    s,
				loading:    false,
				err:        fmt.Errorf("InfluxDB mode requires: host, token, org, bucket, weather-measurement, status-measurement"),
				dataSource: SourceInfluxDB,
			}
		}

		// Use InfluxDB mode
		client := influxdb2.NewClient(host, token)
		return model{
			spinner:            s,
			loading:            true,
			dataSource:         SourceInfluxDB,
			client:             client,
			bucket:             bucket,
			org:                org,
			weatherMeasurement: weatherMeas,
			statusMeasurement:  statusMeas,
		}
	}

	// Use UDP mode
	listener, err := NewUDPListener()
	if err != nil {
		return model{
			spinner:    s,
			loading:    false,
			err:        err,
			dataSource: SourceUDP,
		}
	}

	return model{
		spinner:     s,
		loading:     true,
		dataSource:  SourceUDP,
		udpListener: listener,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, tickCmd()}

	if m.dataSource == SourceUDP && m.udpListener != nil {
		m.udpListener.Start(m.udpListener.program)
	} else if m.dataSource == SourceInfluxDB {
		cmds = append(cmds, fetchWeatherInfluxDB(m.client, m.bucket, m.org, m.weatherMeasurement, m.statusMeasurement))
	}

	return tea.Batch(cmds...)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.udpListener != nil {
				m.udpListener.Close()
			}
			if m.client != nil {
				m.client.Close()
			}
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
		// Only fetch on tick for InfluxDB mode (UDP pushes updates)
		if m.dataSource == SourceInfluxDB {
			return m, tea.Batch(
				fetchWeatherInfluxDB(m.client, m.bucket, m.org, m.weatherMeasurement, m.statusMeasurement),
				tickCmd(),
			)
		}
		return m, tickCmd()

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

	var loadingMsg string
	if m.dataSource == SourceUDP {
		loadingMsg = "Waiting for weather data from Tempest station (UDP)..."
	} else {
		loadingMsg = "Loading weather data from InfluxDB..."
	}

	if m.loading {
		return fmt.Sprintf("\n\n   %s %s\n\n", m.spinner.View(), loadingMsg)
	}

	var b strings.Builder

	// Title with data source indicator
	sourceIndicator := ""
	if m.dataSource == SourceUDP {
		sourceIndicator = " 📡"
	} else {
		sourceIndicator = " 🗄️"
	}

	title := titleStyle.Render("🌤  LCAR.earth Weather Station" + sourceIndicator)
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
	m := initialModel()
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Pass program reference to UDP listener if using UDP mode
	if m.udpListener != nil {
		m.udpListener.program = p
	}

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}
