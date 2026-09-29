// SPDX-License-Identifier: BSD-3-Clause
// Package litra implements Logitech Litra Glow's 20-byte HID output reports.
package litra

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

const (
	VendorID   = 0x046d
	ProductID  = 0xc900
	ReportSize = 20
)

type Action struct {
	Description string
	Report      [ReportSize]byte
}

type Preset struct {
	Name  string
	Kind  string
	Value int
}

var Presets = []Preset{
	{"glow", "brightness", 10}, {"dim", "brightness", 20},
	{"normal", "brightness", 40}, {"medium", "brightness", 60},
	{"bright", "brightness", 80}, {"brightest", "brightness", 100},
	{"warmest", "temperature", 2700}, {"warm", "temperature", 3000},
	{"mild", "temperature", 3500}, {"neutral", "temperature", 4000},
	{"cool", "temperature", 5000}, {"cold", "temperature", 5500},
	{"coldest", "temperature", 6500},
}

func Power(on bool) Action {
	a := Action{Description: "off", Report: [ReportSize]byte{0x11, 0xff, 0x04, 0x1c}}
	if on {
		a.Description = "on"
		a.Report[4] = 1
	}
	return a
}

func Brightness(percent int) (Action, error) {
	if percent < 0 || percent > 100 {
		return Action{}, fmt.Errorf("brightness must be between 0 and 100 percent")
	}
	a := Action{Description: fmt.Sprintf("brightness %d%%", percent), Report: [ReportSize]byte{0x11, 0xff, 0x04, 0x4c}}
	// Map the percentage to the device's intensity range, rounding down.
	// Zero percent is the minimum intensity, not power off.
	a.Report[5] = byte(20 + percent*230/100)
	return a, nil
}

func Temperature(kelvin int) (Action, error) {
	if kelvin < 2700 || kelvin > 6500 {
		return Action{}, fmt.Errorf("temperature must be between 2700 and 6500 kelvin")
	}
	a := Action{Description: fmt.Sprintf("temperature %dK", kelvin), Report: [ReportSize]byte{0x11, 0xff, 0x04, 0x9c}}
	binary.BigEndian.PutUint16(a.Report[4:6], uint16(kelvin))
	return a, nil
}

// Parse validates the entire command sequence before any device is opened.
func Parse(args []string) ([]Action, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("provide at least one light command")
	}
	actions := make([]Action, 0, len(args))
	for i := 0; i < len(args); i++ {
		name := args[i]
		switch name {
		case "on", "off":
			actions = append(actions, Power(name == "on"))
		case "brightness", "set_brightness", "temperature", "color", "colour", "set_temperature":
			if i+1 == len(args) {
				return nil, fmt.Errorf("%s needs a value", name)
			}
			i++
			value := strings.TrimSuffix(args[i], "%")
			brightness := name == "brightness" || name == "set_brightness"
			if !brightness {
				value = strings.TrimSuffix(strings.TrimSuffix(args[i], "K"), "k")
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid %s value %q: expected an integer", name, args[i])
			}
			var a Action
			if brightness {
				a, err = Brightness(n)
			} else {
				a, err = Temperature(n)
			}
			if err != nil {
				return nil, err
			}
			actions = append(actions, a)
		default:
			found := false
			for _, p := range Presets {
				if p.Name == name {
					var a Action
					if p.Kind == "brightness" {
						a, _ = Brightness(p.Value)
					} else {
						a, _ = Temperature(p.Value)
					}
					actions = append(actions, a)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("unknown light command %q; run litractl --help", name)
			}
		}
	}
	return actions, nil
}
