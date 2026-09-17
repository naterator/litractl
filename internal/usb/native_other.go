//go:build !darwin && !linux && !windows

// SPDX-License-Identifier: BSD-3-Clause
package usb

import (
	"fmt"
	"runtime"
)

func New() (Backend, error) {
	return nil, fmt.Errorf("native HID is not implemented for %s; supported platforms are macOS, Linux, and Windows", runtime.GOOS)
}
