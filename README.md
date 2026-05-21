  [![Go](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/go.yml/badge.svg)](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/go.yml)
  [![OpenSSF Scorecard](https://img.shields.io/ossf-scorecard/github.com/jbfavre/mikrotik-fleet-autopilot?label=OpenSSF+Scorecard&style=flat-square)](https://scorecard.dev/viewer/?uri=github.com/jbfavre/mikrotik-fleet-autopilot)
  [![CodeQL Advanced](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/codeql.yml/badge.svg)](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/codeql.yml)

# MikroTik Fleet Autopilot

Automate. Control. Scale. Your MikroTik fleet on autopilot.

## Usage

```bash
./mikrotik-fleet-autopilot --help
```

## Prerequisites

- Router identity is the unique fleet key and **must** match the DNS shortname (for example identity `router1` must resolve through your SSH config to `router1.<your-domain>`).
- Device identities must be unique across the fleet.
- DNS records are an administrator responsibility and must be maintained before using non-enroll workflows.
- All commands expect identity-based hosts. The only exception is `enroll`, which may initially connect by IP before identity/DNS are in place.

### Global Options

Available for all commands:

- `--host <host>`, `-H <host>`  MikroTik router identities (comma-separated for multiple routers). If not provided, will auto-discover from `router*.rsc` files in current directory
- `--ssh-user <username>`, `-u <username>` - MikroTik router SSH username (default: "admin")
- `--ssh-password <password>`, `-p <password>` - MikroTik router SSH password
- `--ssh-passphrase <passphrase>`, `-P <passphrase>` - User private SSH key passphrase
- `--debug` - Enable debug logging
- `--buffered-output` - Force buffered host progress output with deterministic final flush (default prefers live output on TTY)

**Example:**
```bash
mikrotik-fleet-autopilot --host router1,router2 --ssh-user admin --ssh-password secret --debug export
```

### Available Commands

#### discover
Discover LLDP topology across the configured routers.

```bash
mikrotik-fleet-autopilot discover [options]
```

MNDP handling is identity-centric and multi-homed aware:
- one node per identity
- all MNDP interface sightings are merged for that identity
- every MNDP node always renders interface metadata lines prefixed with `📡`
- LLDP edge metadata remains independent from MNDP node metadata

**Options:**
- `--connected-to <device-identity>` - Add a synthetic `mfa` node to the topology graph and show it as connected to the specified topology node identity. This can be a discovered LLDP device identity or a configured source host already present in the graph.

**Examples:**
```bash
# Discover topology from configured routers
mikrotik-fleet-autopilot discover

# Show the local mfa computer as connected to a topology node identity
mikrotik-fleet-autopilot discover --connected-to router1
```

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
mikrotik-fleet-autopilot --host router1,router2 export
```

#### updates
Check for and optionally apply MikroTik RouterOS and RouterBoard updates.

```bash
mikrotik-fleet-autopilot updates [options]
```

**Options:**
- `--updates-apply` - Automatically download and install available updates (default: false, check only)

**Examples:**
```bash
# Check for updates (no installation)
mikrotik-fleet-autopilot updates

# Check and apply updates
mikrotik-fleet-autopilot updates --updates-apply

# Update specific routers
mikrotik-fleet-autopilot --host router1 updates --updates-apply
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
