// SPDX-License-Identifier: BSD-3-Clause
// Package hidcmd implements ordered low-level HID operations.
package hidcmd

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const Help = `Execute HID operations in argument order, using native Go HID backends.

Usage: litracli hid [operations...]

  --vidpid VID/PID              Filter hexadecimal vendor/product IDs (0 = any)
  --usagePage N, --usage N      Filter usage page/usage (decimal, 0xHEX, bare hex)
  --serial TEXT                Filter serial number
  --list                       List matching devices
  --list-usages                List devices with usage page and usage
  --list-detail                List all device details and paths
  --list-json                  List device details as JSON
  --open                       Open the first matching device
  --open-path PATH             Open a specific HID path
  --close                      Close the current device
  --get-report-descriptor      Read the HID report descriptor
  --send-output BYTES          Write an output report (alias: --send-out)
  --send-feature BYTES         Write a feature report
  --read-feature ID            Read a feature report
  --read-input                 Read an input report (alias: --read-in)
  --read-input-forever          Read input reports until interrupted
  --read-input-report ID       Read the specified input report
  --read-input-report-forever ID  Repeatedly read the specified input report
  --length N, -l N             Report length (default 64; 0 = infer on write)
  --timeout MS, -t MS          Input timeout (default 250; -1 = wait indefinitely)
  --base N, -b N               Print bytes in base 10 or 16 (default 16)
  --width N, -w N              Bytes per output line (default 32)
  --quiet, -q                  Only print requested data/listings
  --verbose, -v                Print operation settings
  --version                   Print litracli version and native backend
  --help, -h                   Show this help

Bytes are comma/space separated decimal or 0x-prefixed hex values. The first
byte is the report ID (0 for unnumbered reports). Reports are zero-padded to
--length. All arguments are validated before any USB operations take place.
Read commands print only received bytes; a timeout prints no fabricated data.

Example:
  litracli hid --vidpid 046D/C900 --usagePage 0xff43 --open --length 20 --send-output 0x11,0xff,0x04,0x1c,1 --close
`

type settings struct {
	vendor, product, page, usage uint16
	serial                       string
	length, timeout, base, width int
	quiet, verbose               bool
}

type operation struct {
	name, value string
	settings    settings
	data        []byte
	id          byte
}

type Program struct{ operations []operation }

var aliases = map[string]string{
	"-l": "--length", "-t": "--timeout", "-b": "--base", "-w": "--width",
	"-q": "--quiet", "-v": "--verbose", "-h": "--help",
	"--send-out": "--send-output", "--read-in": "--read-input",
	"--usage-page": "--usagePage",
}

var takesValue = map[string]bool{
	"--vidpid": true, "--usagePage": true, "--usage": true, "--serial": true,
	"--open-path": true, "--length": true, "--timeout": true, "--base": true,
	"--width": true, "--send-output": true, "--send-feature": true,
	"--read-feature": true, "--read-input-report": true, "--read-input-report-forever": true,
}

func Parse(args []string) (*Program, error) {
	p := &Program{}
	cfg := settings{length: 64, timeout: 250, base: 16, width: 32}
	opened := false
	// Copy because expanding short option groups must not modify caller arguments.
	args = append([]string(nil), args...)
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "--") && len(name) > 2 {
			short, rest := name[:2], name[2:]
			if canonical, ok := aliases[short]; ok {
				if takesValue[canonical] {
					name, value, hasValue = short, rest, true
				} else {
					if hasValue {
						return nil, fmt.Errorf("%s does not take a value", short)
					}
					args = append(args[:i+1], append([]string{"-" + rest}, args[i+1:]...)...)
					name = short
				}
			}
		}
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		if takesValue[name] {
			if !hasValue {
				if i+1 == len(args) {
					return nil, fmt.Errorf("%s needs a value", name)
				}
				i++
				value = args[i]
			}
			if value == "" {
				return nil, fmt.Errorf("%s needs a nonempty value", name)
			}
		} else if hasValue {
			return nil, fmt.Errorf("%s does not take a value", name)
		}
		op := operation{name: name, value: value}
		switch name {
		case "--vidpid":
			var err error
			cfg.vendor, cfg.product, err = parseIDs(value)
			if err != nil {
				return nil, err
			}
			continue
		case "--usagePage", "--usage":
			n, err := number(value, 16, true)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if name == "--usagePage" {
				cfg.page = uint16(n)
			} else {
				cfg.usage = uint16(n)
			}
			continue
		case "--serial":
			cfg.serial = value
			continue
		case "--length", "--timeout", "--base", "--width":
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("%s needs an integer", name)
			}
			switch name {
			case "--length":
				if n < 0 || n > 4096 {
					return nil, fmt.Errorf("length must be between 0 and 4096")
				}
				cfg.length = n
			case "--timeout":
				if n < -1 || n > 2147483647 {
					return nil, fmt.Errorf("timeout must be -1 or 0..2147483647 milliseconds")
				}
				cfg.timeout = n
			case "--base":
				if n != 10 && n != 16 {
					return nil, fmt.Errorf("base must be 10 or 16")
				}
				cfg.base = n
			case "--width":
				if n < 1 || n > 4096 {
					return nil, fmt.Errorf("width must be between 1 and 4096")
				}
				cfg.width = n
			}
			continue
		case "--quiet":
			cfg.quiet = true
			continue
		case "--verbose":
			cfg.verbose = true
			continue
		case "--open", "--open-path":
			if opened {
				return nil, fmt.Errorf("use --close before opening another device")
			}
			opened = true
		case "--close":
			opened = false
		case "--list", "--list-usages", "--list-detail", "--list-json", "--version", "--help":
		case "--get-report-descriptor", "--send-output", "--send-feature", "--read-feature", "--read-input", "--read-input-forever", "--read-input-report", "--read-input-report-forever":
			if !opened {
				return nil, fmt.Errorf("%s requires --open or --open-path first", name)
			}
			if name == "--send-output" || name == "--send-feature" {
				data, err := parseBytes(value)
				if err != nil {
					return nil, err
				}
				if cfg.length == 0 {
					cfg.length = len(data)
				}
				if len(data) > cfg.length {
					return nil, fmt.Errorf("%d data bytes exceed report length %d", len(data), cfg.length)
				}
				op.data = make([]byte, cfg.length)
				copy(op.data, data)
			} else if name != "--get-report-descriptor" && cfg.length == 0 {
				return nil, fmt.Errorf("%s needs a nonzero --length", name)
			}
			if takesValue[name] && strings.HasPrefix(name, "--read-") {
				n, err := number(value, 8, false)
				if err != nil {
					return nil, fmt.Errorf("invalid report ID: %w", err)
				}
				op.id = byte(n)
			}
		default:
			return nil, fmt.Errorf("unknown HID option %q; run litracli hid --help", name)
		}
		op.settings = cfg
		p.operations = append(p.operations, op)
	}
	if len(p.operations) == 0 {
		return nil, fmt.Errorf("provide a HID operation, such as --list")
	}
	return p, nil
}

func number(s string, bits int, allowBareHex bool) (uint64, error) {
	base := 10
	if strings.HasPrefix(strings.ToLower(s), "0x") {
		s, base = s[2:], 16
	} else if allowBareHex && strings.ContainsAny(s, "abcdefABCDEF") {
		base = 16
	}
	n, err := strconv.ParseUint(s, base, bits)
	if err != nil {
		return 0, fmt.Errorf("invalid %d-bit unsigned number %q", bits, s)
	}
	return n, nil
}

func parseIDs(s string) (uint16, uint16, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune("/,:", r) || unicode.IsSpace(r) })
	if len(parts) < 1 || len(parts) > 2 {
		return 0, 0, fmt.Errorf("invalid VID/PID %q", s)
	}
	values := [2]uint16{}
	for i, part := range parts {
		n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(part), "0x"), 16, 16)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid hexadecimal VID/PID %q", s)
		}
		values[i] = uint16(n)
	}
	return values[0], values[1], nil
}

func parseBytes(s string) ([]byte, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(parts) == 0 || len(parts) > 4096 {
		return nil, fmt.Errorf("provide between 1 and 4096 report bytes")
	}
	data := make([]byte, len(parts))
	for i, p := range parts {
		n, err := number(p, 8, false)
		if err != nil {
			return nil, fmt.Errorf("report byte %d: %w", i+1, err)
		}
		data[i] = byte(n)
	}
	return data, nil
}
