[![Go](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/go.yml/badge.svg)](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/go.yml) [![SLSA Scorecard](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/scorecard.yml/badge.svg)](https://scorecard.dev/viewer/?uri=github.com/jbfavre/mikrotik-fleet-autopilot) [![CodeQL Advanced](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/codeql.yml/badge.svg)](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/codeql.yml)

# MikroTik Fleet Autopilot

Automate. Control. Scale. Your MikroTik fleet on autopilot.

## Usage

```bash
./mikrotik-fleet-autopilot --help
```

### Global Options

Available for all commands:

- `--host <hosts>` - MikroTik router hostname or IP address (comma-separated for multiple routers). If not provided, will auto-discover from `router*.rsc` files in current directory
- `--user <username>` - MikroTik router username (default: `admin`)
- `--password <password>` - MikroTik router password
- `--debug` - Enable debug logging

**Example:**
```bash
mikrotik-fleet-autopilot --host router1.local,192.168.1.1 --user admin --password secret --debug export
```

### Available Commands

#### export
Export MikroTik router configuration to `.rsc` files.

```bash
mikrotik-fleet-autopilot export [options]
```

**Options:**
- `--show-sensitive` - Include sensitive information (passwords, secrets) in the export
- `--output-dir <dir>` - Directory where to save the exported configuration (default: current directory)

**Examples:**
```bash
# Export configuration for auto-discovered routers
mikrotik-fleet-autopilot export

# Export with sensitive data to a specific directory
mikrotik-fleet-autopilot export --show-sensitive --output-dir ./backups

# Export specific routers
mikrotik-fleet-autopilot --host router1.home,router2.home export
```

#### updates
Check for and optionally apply MikroTik RouterOS and RouterBoard updates.

```bash
mikrotik-fleet-autopilot updates [options]
```

**Options:**
- `--apply-updates` - Automatically download and install available updates (default: false, check only)

**Examples:**
```bash
# Check for updates (no installation)
mikrotik-fleet-autopilot updates

# Check and apply updates
mikrotik-fleet-autopilot updates --apply-updates

# Update specific routers
mikrotik-fleet-autopilot --host 192.168.1.1 updates --apply-updates
```

## Building

```bash
make build
```

## Development

### Running Tests

Run all tests:
```bash
make test # runs go test -v ./... behind the scene
```

Run tests with coverage:
```bash
make test-coverage
```

### Running Benchmarks

Run all benchmarks:
```bash
make test-benchmark
```
