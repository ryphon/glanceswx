# GlancesWX

A beautiful terminal UI (TUI) weather station monitor for WeatherFlow Tempest data, built with the Charm suite of libraries (Bubble Tea, Lipgloss).

## Features

- **Real-time weather data display:**
  - Temperature (°F)
  - Humidity (%)
  - Barometric pressure (mb)
  - Wind speed (mph)
  - Wind gust (24h max)
  - Wind gust (all-time max)
  - UV index
  - Solar radiation (W/m²)
  - 24-hour precipitation (inches)
  - Battery voltage
  - Station uptime

- **Visual compass:** Shows current wind direction with highlighted cardinal directions and arrow indicator

- **Auto-refreshes:** Updates every 2 seconds

## Requirements

- **Go 1.23+** for building
- **InfluxDB 2.x** with WeatherFlow Tempest data
- **WeatherFlow Tempest weather station** data being ingested into InfluxDB

## Setup

### 1. Clone or download this repository

```bash
git clone <repo-url>
cd glanceswx
```

### 2. Configure environment variables

Copy the example environment file:

```bash
cp .env.example .env
```

Edit `.env` with your configuration:

```bash
# InfluxDB Configuration
INFLUX_HOST=https://your-influx-host.com
INFLUX_PORT=443
INFLUX_ORG=your-org-id
INFLUX_BUCKET=your-bucket-name
INFLUX_TOKEN=your-influx-token

# WeatherFlow Tempest Station IDs
WEATHER_MEASUREMENT=ST-00000000_observation
STATUS_MEASUREMENT=ST-00000000_status
```

#### Finding your station IDs:

Your WeatherFlow Tempest creates measurements in InfluxDB with names like:
- `ST-XXXXXXXX_observation` - weather observations
- `ST-XXXXXXXX_status` - station status (battery, uptime)

Replace `ST-XXXXXXXX` with your actual station ID. You can find this by:
1. Querying your InfluxDB bucket for measurements
2. Looking at the WeatherFlow app or dashboard
3. Checking your Home Assistant entity IDs if using that integration

### 3. Build the application

```bash
go mod download
go build -o glanceswx
```

### 4. Run

```bash
./glanceswx
```

Press `q` or `Ctrl+C` to quit.

## InfluxDB Queries

The application queries your InfluxDB instance with the following Flux queries:

### Current Weather Data
```flux
from(bucket: "your-bucket")
  |> range(start: -5m)
  |> filter(fn: (r) => r._measurement == "ST-XXXXXXXX_observation")
  |> last()
```

**Fields used:**
- `temperature_air` - Converted from Celsius to Fahrenheit
- `r-humidity` - Relative humidity percentage
- `pressure_station` - Converted from Pascals to millibars
- `wind_speed` - Converted from m/s to mph
- `wind_direction` - Degrees (0-360)
- `uv` - UV index
- `irradiance_sun` - Solar radiation in W/m²
- `rain_accumulation` - Converted from mm to inches

### Wind Gust (24h Max)
```flux
from(bucket: "your-bucket")
  |> range(start: -24h)
  |> filter(fn: (r) => r._measurement == "ST-XXXXXXXX_observation")
  |> filter(fn: (r) => r._field == "wind_gust")
  |> max()
```

### Wind Gust (All-Time Max)
```flux
from(bucket: "your-bucket")
  |> range(start: -365d)
  |> filter(fn: (r) => r._measurement == "ST-XXXXXXXX_observation")
  |> filter(fn: (r) => r._field == "wind_gust")
  |> max()
```

### Station Status
```flux
from(bucket: "your-bucket")
  |> range(start: -5m)
  |> filter(fn: (r) => r._measurement == "ST-XXXXXXXX_status")
  |> last()
```

**Fields used:**
- `voltage` - Battery voltage
- `uptime` - Station uptime in seconds

## Running from Anywhere

If you want to run the binary from anywhere on your system:

### Option 1: Add to PATH
```bash
sudo cp glanceswx /usr/local/bin/
```

Then export environment variables in your shell config (`~/.bashrc`, `~/.zshrc`, etc.):
```bash
export INFLUX_HOST=https://your-influx-host.com
export INFLUX_PORT=443
export INFLUX_ORG=your-org-id
export INFLUX_BUCKET=your-bucket-name
export INFLUX_TOKEN=your-influx-token
export WEATHER_MEASUREMENT=ST-00000000_observation
export STATUS_MEASUREMENT=ST-00000000_status
```

### Option 2: Wrapper Script
Create a script (e.g., `~/bin/wx`):
```bash
#!/bin/bash
cd /home/dx/code/glanceswx && ./glanceswx
```

Make it executable:
```bash
chmod +x ~/bin/wx
```

## Troubleshooting

### "Missing required environment variables"
- Ensure all environment variables in `.env` are set
- If running from another directory, make sure the `.env` file is being loaded or export the variables

### "No data showing"
- Verify your InfluxDB connection with a test query
- Check that your bucket name and measurement names are correct
- Ensure data is actively being written to InfluxDB from your WeatherFlow station

### "Cannot connect to InfluxDB"
- Verify `INFLUX_HOST` includes the protocol (`https://` or `http://`)
- Check that your InfluxDB token has read permissions for the bucket
- Ensure network connectivity to your InfluxDB instance

## License

MIT
