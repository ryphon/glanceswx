# GlancesWX

A beautiful terminal UI (TUI) weather station monitor for WeatherFlow Tempest data, built with the Charm suite of libraries (Bubble Tea, Lipgloss).

Supports two data sources:
- **📡 UDP Mode** - Listens directly to UDP broadcasts from your Tempest station (port 50222)
- **🗄️ InfluxDB Mode** - Queries weather data from an InfluxDB instance

The app automatically detects which mode to use based on environment variables.

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

- **Data source indicator:** Shows 📡 for UDP mode or 🗄️ for InfluxDB mode in the title

- **Auto-refreshes:**
  - UDP mode: Updates in real-time as broadcasts are received
  - InfluxDB mode: Polls every 2 seconds

## Requirements

- **Go 1.23+** for building
- **WeatherFlow Tempest weather station**
- For UDP mode: Station on the same local network
- For InfluxDB mode: InfluxDB 2.x instance with Tempest data

## Setup

### 1. Clone or download this repository

```bash
git clone <repo-url>
cd glanceswx
```

### 2. Build the application

```bash
go mod download
go build -o glanceswx
```

### 3. Choose your data source

#### Option A: UDP Mode (Default, No Configuration Needed)

Simply run without any environment variables:

```bash
./glanceswx
```

The app will listen on UDP port 50222 for broadcasts from your Tempest station. Data appears as soon as the station sends its next update (typically every minute).

#### Option B: InfluxDB Mode

Copy the example environment file and configure:

```bash
cp .env.example .env
```

Edit `.env` with your InfluxDB configuration:

```bash
INFLUX_HOST=https://your-influx-host.com
INFLUX_PORT=443
INFLUX_ORG=your-org-id
INFLUX_BUCKET=your-bucket-name
INFLUX_TOKEN=your-influx-token
WEATHER_MEASUREMENT=ST-00000000_observation
STATUS_MEASUREMENT=ST-00000000_status
```

Then run:

```bash
./glanceswx
```

The app will automatically use InfluxDB mode when all required environment variables are set.

#### Command-Line Flags

You can also configure `glanceswx` using command-line flags. Note that environment variables will take precedence over command-line flags if both are provided for the same setting.

```bash
./glanceswx --help
```

**Available Flags:**

- `--mode`: Data source mode: `udp` or `influxdb`. (Default: `udp`)
- `--influx-host`: InfluxDB host URL.
- `--influx-token`: InfluxDB token.
- `--influx-org`: InfluxDB organization.
- `--influx-bucket`: InfluxDB bucket.
- `--weather-measurement`: Weather observation measurement name. (Default: `weather`)
- `--status-measurement`: Status measurement name. (Default: `status`)

**Example for InfluxDB mode using flags:**

```bash
./glanceswx --mode=influxdb \
  --influx-host=https://your-influx-host.com \
  --influx-token=your-influx-token \
  --influx-org=your-org-id \
  --influx-bucket=your-bucket-name
```

Press `q` or `Ctrl+C` to quit.

## How It Works

### UDP Mode (📡)

The app listens for UDP broadcasts on port 50222, which the Tempest weather station sends to your local network. It parses two types of messages:

**Observation Messages (`obs_st`)**
- Temperature (converted from Celsius to Fahrenheit)
- Humidity
- Barometric pressure
- Wind speed and direction (converted from m/s to mph)
- Wind gust (tracked in-memory for 24h max and all-time max)
- UV index
- Solar radiation
- Precipitation accumulation (converted from mm to inches)
- Battery voltage

**Status Messages (`device_status`)**
- Battery voltage
- Station uptime

### InfluxDB Mode (🗄️)

The app queries your InfluxDB instance every 2 seconds using Flux queries to retrieve:
- Current weather observations from your specified measurement
- 24-hour and all-time wind gust maximums
- Station status (battery, uptime)

## Running from Anywhere

If you want to run the binary from anywhere on your system:

```bash
sudo cp glanceswx /usr/local/bin/
```

Then you can run it from any directory:
```bash
glanceswx
```

## Troubleshooting

### UDP Mode Issues

**"No data showing" or "Waiting for weather data"**
- Verify your Tempest station is on the same network
- Check that UDP broadcasts are not blocked by a firewall
- Tempest stations broadcast observations every minute - wait at least 60 seconds
- Ensure the station is powered on and online
- Look for the 📡 indicator in the title to confirm UDP mode is active

**"failed to listen on UDP port 50222"**
- Port 50222 may already be in use by another application
- Try stopping other weather monitoring applications
- On Linux/Mac, you may need elevated permissions: `sudo ./glanceswx`

### InfluxDB Mode Issues

**"No data showing"**
- Verify your InfluxDB connection with a test query
- Check that your bucket name and measurement names are correct
- Ensure data is actively being written to InfluxDB from your Tempest station
- Look for the 🗄️ indicator in the title to confirm InfluxDB mode is active

**"Cannot connect to InfluxDB"**
- Verify `INFLUX_HOST` includes the protocol (`https://` or `http://`)
- Check that your InfluxDB token has read permissions for the bucket
- Ensure network connectivity to your InfluxDB instance

## License

MIT
