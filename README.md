# litractl

A standalone Go CLI for USB-connected **Logitech Litra Glow** lights
(`046D:C900`). The project and executable are named `litractl`.

```sh
litractl on normal warm
litractl off
litractl brightness 65 temperature 4500
```

Light controls and low-level HID operations use the operating system's device
APIs: IOKit/CoreFoundation on macOS, kernel `hidraw` on Linux, and Windows HID.

## Build and run

Requires Go 1.26 or newer.

```sh
make build
./litractl list
./litractl on glow warmest
```

The resulting file can be moved anywhere on your PATH. `litractl license`
displays the project license.

## Light commands

Commands are applied **in order to every connected Glow** by default. Use
`--serial SERIAL` or `--path PATH` to target a particular light. If both are
supplied, both must match. `list` shows each usable light once, even when its
HID interface exposes multiple collections. `list --json` and
`hid --list-json` describe devices the same way, with vendor, product and
usage IDs as hexadecimal strings such as `"0x046D"`.

```sh
litractl list --json
litractl --serial SERIAL on normal warm
litractl --path 'DevSrvsID:123456789' off
litractl --dry-run on normal warm
litractl --quiet on
```

| Commands | Setting |
| --- | --- |
| `on`, `off` | Power |
| `glow`, `dim`, `normal`, `medium`, `bright`, `brightest` | 10%, 20%, 40%, 60%, 80%, 100% brightness |
| `warmest`, `warm`, `mild`, `neutral`, `cool`, `cold`, `coldest` | 2700, 3000, 3500, 4000, 5000, 5500, 6500K |
| `brightness PERCENT` | Integer from 0 through 100; optional `%` suffix |
| `temperature KELVIN` | Integer from 2700 through 6500; optional `K` suffix |
| `color KELVIN`, `colour KELVIN` | Aliases for temperature |
| `set_brightness PERCENT`, `set_temperature KELVIN` | Aliases for brightness and temperature |
| `set COMMAND ...` | Explicit command for a sequence, e.g. `set on normal warm` |

Brightness maps to the device's intensity range using `floor(20 + percent*230/100)`.
**0% is the hardware's minimum intensity, not off.** Glow has adjustable white
color temperature, not RGB color.

The complete sequence is validated before opening any USB device. Unknown
commands, out-of-range values, missing devices, USB errors, and short writes
return a nonzero exit status. If one light fails, the other selected lights
are still attempted, and the command reports the partial failure.

`--dry-run` prints the exact 20-byte reports without accessing hardware.
Normal success output means the OS accepted the output report. The opt-in
hardware test also queries the device to verify the resulting settings.

## Low-level HID commands

`litractl hid` lists HID devices and sends or reads individual reports:

```sh
litractl hid --vidpid 046D/C900 --list-detail
litractl hid --vidpid 046D/C900 --usagePage 0xff43 --open \
  --length 20 --send-output 0x11,0xff,0x04,0x1c,1 --close
litractl hid --vidpid 046D/C900 --usagePage 0xff43 --open \
  --get-report-descriptor --close
litractl hid --help
```

Supported operations:

- Filters: `--vidpid`, `--usagePage` / `--usage-page`, `--usage`, `--serial`.
- Enumeration: `--list`, `--list-usages`, `--list-detail`, `--list-json`.
- Handles: `--open`, `--open-path`, `--close`; multiple open/close cycles.
- Reports: `--send-output` / `--send-out`, `--send-feature`, `--read-feature`,
  `--read-input` / `--read-in`, `--read-input-report`,
  `--get-report-descriptor`.
- Continuous reads: `--read-input-forever`, `--read-input-report-forever`;
  Ctrl-C cancels polling and closes the handle.
- Formatting: `--length` / `-l`, `--timeout` / `-t`, `--base` / `-b`,
  `--width` / `-w`, `--quiet` / `-q`, `--verbose` / `-v`, `--version`.

Settings take effect in argument order. Defaults are 64-byte reports, a 250ms
input timeout, base 16, and 32 bytes per line. `--length 0` infers length from
the next write. Reports include a leading ID byte (`0` for unnumbered reports)
and are zero-padded to the configured length. `--timeout -1` waits for input
until interrupted. Vendor/product IDs are hexadecimal. Usage pages and usages
are decimal unless they are `0x`-prefixed, zero-padded (`0202`), or contain
A–F. Report data accepts decimal or `0x`-prefixed hexadecimal bytes, separated
by commas or spaces.

Arguments are fully validated before USB access. Invalid bytes and oversized
reports are rejected, failures return errors, and reads print only received
bytes. `--open`
honors the serial filter and chooses the first matching device. Close the
current handle before opening another. Version output identifies the native
backend. macOS paths use `DevSrvsID:<registry ID>`.

| Platform | Light controls and report I/O | Raw report descriptor |
| --- | --- | --- |
| macOS amd64/arm64 | Implemented; tested on attached lights on arm64 | Supported |
| Linux amd64/arm64 | Implemented; hardware testing pending | Supported |
| Windows amd64/arm64 | Implemented; hardware testing pending | Not supported by this backend |

The Windows backend uses documented HID APIs, which expose parsed capabilities
rather than the original descriptor. It reports an explicit error for
`--get-report-descriptor`; it does not fabricate a descriptor. Other platforms
report that native HID is unsupported. Feature reports also require a device
whose descriptor supports them; the Glow's control interface uses output and
input reports.

## Updates

```sh
litractl update --check   # check only; also: litractl update check
litractl update           # install a newer stable release over this executable
```

The updater checks `naterator/litractl` GitHub Releases, selects the matching platform,
downloads over HTTPS, checks the expected size and SHA-256 checksum, and
validates the Go module/command identity and target OS/architecture before
replacing the executable. Unix replacement is atomic. Windows replacement
uses a backup and rollback; an old executable can remain as `.old` while it is
running. Symlink targets and executable permissions are preserved.

Release tags must be stable semantic versions such as `v1.0.0`. A local `dev`
build can update to a published stable version. Until a stable release and its
assets exist, the checker reports that no release is available. Checking never
installs anything, and ordinary light commands do not contact the network.

Assets must be named `litractl-OS-ARCH` (`.exe` on Windows), with a corresponding
`ASSET.sha256`. For example, `litractl-darwin-arm64` and
`litractl-darwin-arm64.sha256`.

## CI and verification

GitHub Actions runs the following checks and release jobs:

- Push/PR builds run formatting, tests, vet, and a standalone build on
  macOS, Linux, and Windows.
- Published releases upload docs/licenses and six raw binaries with SHA-256
  checksum files (amd64/arm64 on all three OSes).
- `govulncheck` runs on pull requests, pushes to `main`, and weekly.

```sh
make test
make vet
make test-hardware  # explicit opt-in: changes all attached Glows, then restores them
```

Normal tests require no lights and cover report encoding,
command chaining/validation, selection/deduplication, partial device failures,
HID report operations, cancellation, and updater download/replacement failures.
Updater tests use temporary files and local HTTPS servers. The hardware test
captures each light's power/brightness/temperature, tests changes, verifies USB
readback, and restores the original raw values even if a check fails. It also
logs the temperature each light reports after a 4550K request, to show whether
the hardware keeps values that are not multiples of 100K.

## Linux access

The kernel must expose the lights as `/dev/hidraw*`, and your account must have
read/write permission. For a desktop with systemd/udev, a narrowly scoped rule
can grant access to the active user:

```udev
SUBSYSTEM=="hidraw", ATTRS{idVendor}=="046d", ATTRS{idProduct}=="c900", TAG+="uaccess"
```

Place it in `/etc/udev/rules.d/70-litractl.rules`, reload udev rules, and
reconnect the lights. On a headless system, use a device-access group appropriate
to that installation. The program does not install or modify system rules.

## Source layout

- `cmd/litractl`: executable entry point and version injection.
- `internal/cli`: Cobra subcommands.
- `internal/license`: embedded project license.
- `internal/litra`: light protocol, selectors, and hardware integration test.
- `internal/hidcmd`: ordered generic HID operation parser/runner.
- `internal/usb`: native Go backends and HID descriptor parser.
- `internal/update`: release checker, installer, and rollback tests.
