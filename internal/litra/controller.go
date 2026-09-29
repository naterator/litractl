// SPDX-License-Identifier: BSD-3-Clause
package litra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/naterator/litractl/internal/usb"
)

type Selector struct{ Serial, Path string }

// name identifies a light by serial number, or by path when it has none.
func name(d usb.Info) string {
	if d.Serial == "" {
		return d.Path
	}
	return d.Serial
}

// describe identifies a light in errors by serial number and path.
func describe(d usb.Info) string {
	if d.Serial == "" {
		return d.Path
	}
	return d.Serial + " (" + d.Path + ")"
}

// Devices selects the vendor control collection, avoiding consumer-control
// collections that appear as separate, non-writable paths on Windows. macOS
// can enumerate the same path more than once, so paths are also deduplicated.
func Devices(b usb.Backend, selectBy Selector) ([]usb.Info, error) {
	all, err := b.Enumerate(VendorID, ProductID)
	if err != nil {
		return nil, fmt.Errorf("enumerate Litra Glow devices: %w", err)
	}
	devices := make([]usb.Info, 0)
	seen := make(map[string]bool)
	for _, d := range all {
		if d.VendorID != VendorID || d.ProductID != ProductID || d.UsagePage != 0xff43 || d.Usage != 0x0202 {
			continue
		}
		if selectBy.Serial != "" && d.Serial != selectBy.Serial {
			continue
		}
		if selectBy.Path != "" && d.Path != selectBy.Path {
			continue
		}
		if d.Path == "" || seen[d.Path] {
			continue
		}
		seen[d.Path] = true
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Path < devices[j].Path })
	return devices, nil
}

// Apply keeps one handle per selected light, applies actions in the requested
// order, and attempts the other lights even when one cannot be opened/written.
// Any partial failure is returned to the caller as a nonzero command result.
func Apply(ctx context.Context, b usb.Backend, devices []usb.Info, actions []Action, out io.Writer) error {
	if len(devices) == 0 {
		return errors.New("no matching Litra Glow lights found (USB 046D/C900); check the connection or selection flags")
	}
	type opened struct {
		info   usb.Info
		device usb.Device
		failed bool
	}
	handles := make([]opened, 0, len(devices))
	var failures []error
	// The action loop reports cancellation once, including cancellation here.
	for _, info := range devices {
		if ctx.Err() != nil {
			break
		}
		d, err := b.Open(info.Path)
		if err != nil {
			failures = append(failures, fmt.Errorf("open %s: %w", describe(info), err))
			continue
		}
		handles = append(handles, opened{info: info, device: d})
	}
	for _, action := range actions {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		for i := range handles {
			h := &handles[i]
			if h.failed {
				continue
			}
			n, err := h.device.Write(action.Report[:])
			if err == nil && n != ReportSize {
				err = io.ErrShortWrite
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("%s on %s: %w", action.Description, describe(h.info), err))
				h.failed = true
				continue
			}
			fmt.Fprintf(out, "%s: %s\n", name(h.info), action.Description)
		}
	}
	for _, h := range handles {
		if err := h.device.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close %s: %w", h.info.Path, err))
		}
	}
	return errors.Join(failures...)
}
