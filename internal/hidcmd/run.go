// SPDX-License-Identifier: BSD-3-Clause
package hidcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/naterator/litractl/internal/usb"
)

func (p *Program) Run(ctx context.Context, b usb.Backend, out io.Writer, version string) (result error) {
	var d usb.Device
	defer func() {
		if d != nil {
			result = errors.Join(result, d.Close())
		}
	}()
	for _, op := range p.operations {
		if err := ctx.Err(); err != nil {
			return err
		}
		cfg := op.settings
		if cfg.verbose && !cfg.quiet {
			fmt.Fprintf(out, "%s: length=%d timeout=%d base=%d width=%d\n", op.name, cfg.length, cfg.timeout, cfg.base, cfg.width)
		}
		switch op.name {
		case "--help":
			fmt.Fprint(out, Help)
		case "--version":
			fmt.Fprintf(out, "litracli %s\nBackend: %s\n", version, b.Version())
		case "--list", "--list-usages", "--list-detail", "--list-json":
			devices, err := filtered(b, cfg)
			if err != nil {
				return err
			}
			if err := list(out, op.name, devices); err != nil {
				return err
			}
		case "--open", "--open-path":
			path := op.value
			if op.name == "--open" {
				devices, err := filtered(b, cfg)
				if err != nil {
					return err
				}
				if len(devices) == 0 {
					return errors.New("no HID devices match the current filters")
				}
				path = devices[0].Path
			}
			var err error
			d, err = b.Open(path)
			if err != nil {
				return fmt.Errorf("open %s: %w", path, err)
			}
			if !cfg.quiet {
				fmt.Fprintf(out, "Opened %s\n", path)
			}
		case "--close":
			if d != nil {
				err := d.Close()
				d = nil
				if err != nil {
					return err
				}
				if !cfg.quiet {
					fmt.Fprintln(out, "Closed device")
				}
			}
		case "--send-output", "--send-feature":
			var n int
			var err error
			if op.name == "--send-output" {
				n, err = d.Write(op.data)
			} else {
				n, err = d.SendFeatureReport(op.data)
			}
			if err == nil && n != len(op.data) {
				err = io.ErrShortWrite
			}
			if err != nil {
				return fmt.Errorf("%s: %w", op.name, err)
			}
			if !cfg.quiet {
				fmt.Fprintf(out, "Wrote %d bytes:\n", n)
				printBytes(out, op.data, cfg)
			}
		default:
			if err := read(ctx, d, op, out); err != nil {
				return fmt.Errorf("%s: %w", op.name, err)
			}
		}
	}
	return nil
}

func filtered(b usb.Backend, cfg settings) ([]usb.Info, error) {
	all, err := b.Enumerate(cfg.vendor, cfg.product)
	if err != nil {
		return nil, err
	}
	devices := make([]usb.Info, 0)
	for _, d := range all {
		if cfg.vendor != 0 && d.VendorID != cfg.vendor || cfg.product != 0 && d.ProductID != cfg.product {
			continue
		}
		if cfg.page != 0 && d.UsagePage != cfg.page || cfg.usage != 0 && d.Usage != cfg.usage {
			continue
		}
		if cfg.serial != "" && d.Serial != cfg.serial {
			continue
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func list(out io.Writer, mode string, devices []usb.Info) error {
	if mode == "--list-json" {
		// Device identifiers use hexadecimal strings in JSON output.
		rows := make([]map[string]any, 0, len(devices))
		for _, d := range devices {
			rows = append(rows, map[string]any{
				"vendor_id": fmt.Sprintf("0x%04X", d.VendorID), "product_id": fmt.Sprintf("0x%04X", d.ProductID),
				"usage_page": fmt.Sprintf("0x%04X", d.UsagePage), "usage": fmt.Sprintf("0x%04X", d.Usage),
				"manufacturer_string": d.Manufacturer, "product_string": d.Product, "serial_number": d.Serial,
				"interface_number": d.Interface, "bus_type": fmt.Sprint(d.BusType), "bus_type_name": d.Bus, "path": d.Path,
			})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"devices": rows})
	}
	for _, d := range devices {
		if mode == "--list-usages" {
			fmt.Fprintf(out, "%04X/%04X / %04X/%04X: %s - %s\n", d.VendorID, d.ProductID, d.UsagePage, d.Usage, d.Manufacturer, d.Product)
		} else {
			fmt.Fprintf(out, "%04X/%04X: %s - %s\n", d.VendorID, d.ProductID, d.Manufacturer, d.Product)
		}
		if mode == "--list-detail" {
			fmt.Fprintf(out, "  vendorId:      0x%04X\n  productId:     0x%04X\n  usagePage:     0x%04X\n  usage:         0x%04X\n  serial_number: %s\n  interface:     %d\n  bus_type:      %s (%d)\n  path: %s\n\n", d.VendorID, d.ProductID, d.UsagePage, d.Usage, d.Serial, d.Interface, d.Bus, d.BusType, d.Path)
		}
	}
	return nil
}

func read(ctx context.Context, d usb.Device, op operation, out io.Writer) error {
	cfg := op.settings
	forever := strings.HasSuffix(op.name, "-forever")
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		length := cfg.length
		if op.name == "--get-report-descriptor" {
			length = 4096
		}
		buf := make([]byte, length)
		buf[0] = op.id
		var n int
		var err error
		switch op.name {
		case "--get-report-descriptor":
			n, err = d.GetReportDescriptor(buf)
		case "--read-feature":
			n, err = d.GetFeatureReport(buf)
		case "--read-input-report", "--read-input-report-forever":
			n, err = d.GetInputReport(buf)
		case "--read-input", "--read-input-forever":
			n, err = readInput(ctx, d, buf, cfg.timeout)
		default:
			return fmt.Errorf("unsupported operation %s", op.name)
		}
		if err != nil {
			return err
		}
		if n < 0 || n > len(buf) {
			return fmt.Errorf("invalid HID read length %d", n)
		}
		if !cfg.quiet {
			fmt.Fprintf(out, "Read %d bytes:\n", n)
		}
		if n != 0 {
			printBytes(out, buf[:n], cfg)
		}
		if !forever {
			return nil
		}
		// GetInputReport is a control transfer; pace repeated requests. Also
		// avoid busy-spinning when interrupt reads were configured nonblocking.
		if op.name == "--read-input-report-forever" || cfg.timeout == 0 {
			pause := time.Duration(cfg.timeout) * time.Millisecond
			if pause <= 0 {
				pause = time.Millisecond
			}
			if err := wait(ctx, pause); err != nil {
				return err
			}
		}
	}
}

func readInput(ctx context.Context, d usb.Device, buf []byte, timeout int) (int, error) {
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		period := 100 * time.Millisecond
		if timeout >= 0 {
			period = min(period, max(0, time.Until(deadline)))
		}
		n, err := d.ReadWithTimeout(buf, period)
		if err != nil || n > 0 {
			return n, err
		}
		if timeout >= 0 && !time.Now().Before(deadline) {
			return 0, nil
		}
	}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func printBytes(out io.Writer, data []byte, cfg settings) {
	for i, value := range data {
		if cfg.base == 10 {
			fmt.Fprintf(out, " %3d", value)
		} else {
			fmt.Fprintf(out, " %02X", value)
		}
		if (i+1)%cfg.width == 0 || i+1 == len(data) {
			fmt.Fprintln(out)
		}
	}
}
