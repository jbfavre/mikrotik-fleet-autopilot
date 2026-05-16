  [![Go](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/go.yml/badge.svg)](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/go.yml)
  [![OpenSSF Scorecard](https://img.shields.io/ossf-scorecard/github.com/jbfavre/mikrotik-fleet-autopilot?label=OpenSSF+Scorecard&style=flat-square)](https://scorecard.dev/viewer/?uri=github.com/jbfavre/mikrotik-fleet-autopilot)
  [![CodeQL Advanced](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/codeql.yml/badge.svg)](https://github.com/jbfavre/mikrotik-fleet-autopilot/actions/workflows/codeql.yml)

# MikroTik Fleet Autopilot

Automate. Control. Scale. Your MikroTik fleet on autopilot.

## Prerequisites

This tool now treats the MikroTik `/system identity` value as the single primary key for every enrolled device.

- Device identities must be unique across the fleet. Discovery fails with a hard error if two devices share the same identity.
- Device identities must resolve in DNS. Discovery validates that the identity resolves before attempting SSH.
- SSH connections must rely on `~/.ssh/config` to translate the identity to its FQDN and resolved IP.
- IP addresses are metadata only after enrollment. The only exception is `enroll`, which may connect to a device by IP before DNS exists.

Example identity stack:

| MikroTik identity | DNS record | `~/.ssh/config` host | Host key file | Config backup |
| --- | --- | --- | --- | --- |
| `router1` | `router1.home` | `Host router1` → `Hostname router1.home` | `router1.hostkey` | `router1.rsc` |
| `router2` | `router2.home` | `Host router2` → `Hostname router2.home` | `router2.hostkey` | `router2.rsc` |

## Usage

```bash
./mikrotik-fleet-autopilot --help
```

### Global Options

Available for all commands:

- `--host <host>`, `-H <host>`  MikroTik device identity (short hostname, comma-separated for multiple routers). If not provided, the tool auto-discovers identities from `router*.rsc` files in the current directory. Use an IP address only with `enroll` for initial enrollment.
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

#### enroll
Enroll a factory-reset MikroTik router. This is the only command that may use an IP address as the connection target.

```bash
mikrotik-fleet-autopilot --host <ip-or-identity> enroll [options]
```

**Options:**
- `--hostname <identity>` - Device identity to set during enrollment (required unless using `--update-hostkey-only`)
- `--new-password <password>` - New admin password (required for full enrollment)
- `--pre-enroll-script <path>` - RouterOS script applied before identity is set (default: `./pre-enroll.rsc`)
- `--post-enroll-script <path>` - RouterOS script applied after export (default: `./post-enroll.rsc`)
- `--skip-updates` - Skip update checks during enrollment
- `--skip-export` - Skip configuration export after enrollment
- `--output-dir <dir>` - Output directory for exported configuration (default: current directory)
- `--force`, `-f` - Remove existing enrollment artifacts and perform the full enrollment again
- `--update-hostkey-only` - Update the stored SSH host key for already enrolled identity-based hosts

**Examples:**
```bash
# Initial enrollment by IP before DNS exists
mikrotik-fleet-autopilot --host 192.168.1.1 enroll --hostname router1 --new-password secret

# Re-enroll a device by IP after removing the existing identity-based artifacts
mikrotik-fleet-autopilot --host 192.168.1.1 enroll --hostname router1 --new-password secret --force

# Refresh host keys for already enrolled devices by identity
mikrotik-fleet-autopilot --host router1,router2 enroll --update-hostkey-only
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
