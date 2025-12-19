# Test Data Fixtures for Enroll Tests

This directory contains static test fixtures used by the enroll package tests.

## Directory Structure

```
testdata/
├── hostkeys/        # Pre-generated SSH host key files
│   ├── 192.168.1.1.hostkey  # Example RSA host key (old)
│   └── router1.hostkey       # Example ED25519 host key (new)
└── configs/         # RouterOS configuration files
    ├── test-config.rsc   # Valid configuration with commands
    └── empty-config.rsc  # Empty configuration (comments only)
```

## Host Key Files

Host key files follow the format used by the application:

```json
{
  "host": "192.168.1.1",
  "algorithm": "ssh-rsa",
  "fingerprint": "SHA256:...",
  "publicKey": "base64_encoded_key",
  "capturedAt": "2025-12-01T10:00:00Z"
}
```

### Available Fixtures

- **192.168.1.1.hostkey**: Simulates an "old" host key (RSA) used for testing updates
- **router1.hostkey**: Simulates a "new" host key (ED25519) used as the captured key

## Configuration Files

Configuration files are RouterOS script files (.rsc) containing commands:

- **test-config.rsc**: Contains valid RouterOS commands for testing config application
- **empty-config.rsc**: Empty file with only comments for testing edge cases

## Usage in Tests

Tests use `copyFile()` helper to copy these fixtures into temporary test directories:

```go
srcFile := filepath.Join(originalWd, "testdata/hostkeys/192.168.1.1.hostkey")
dstFile := core.HostKeyFilePath(tt.host)
copyFile(srcFile, dstFile)
```

This approach provides:
- **Faster tests**: No RSA key generation needed
- **Predictable data**: Consistent fingerprints and test results
- **Easy maintenance**: Update fixtures once, all tests benefit
