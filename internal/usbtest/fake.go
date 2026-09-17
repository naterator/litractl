// SPDX-License-Identifier: BSD-3-Clause
// Package usbtest supplies deterministic USB fakes for hardware-independent tests.
package usbtest

import (
	"time"

	"github.com/naterator/litractl/internal/usb"
)

type Backend struct {
	Infos                []usb.Info
	Devices              map[string]*Device
	OpenErrors           map[string]error
	Opened               []string
	Enumerations, Closed int
}

func (b *Backend) Enumerate(uint16, uint16) ([]usb.Info, error) {
	b.Enumerations++
	return b.Infos, nil
}
func (b *Backend) Open(path string) (usb.Device, error) {
	b.Opened = append(b.Opened, path)
	if err := b.OpenErrors[path]; err != nil {
		return nil, err
	}
	return b.Devices[path], nil
}
func (*Backend) Version() string { return "fake" }
func (b *Backend) Close() error  { b.Closed++; return nil }

type Device struct {
	Writes, Features      [][]byte
	Reads                 [][]byte
	Descriptor            []byte
	ReportIDs             []byte
	WriteError, ReadError error
	ShortWrite            bool
	Closed                int
	OnRead                func()
}

func (d *Device) Write(p []byte) (int, error) {
	d.Writes = append(d.Writes, append([]byte(nil), p...))
	if d.WriteError != nil {
		return 0, d.WriteError
	}
	if d.ShortWrite {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func (d *Device) SendFeatureReport(p []byte) (int, error) {
	d.Features = append(d.Features, append([]byte(nil), p...))
	return len(p), nil
}
func (d *Device) read(p []byte) (int, error) {
	if d.OnRead != nil {
		d.OnRead()
	}
	if d.ReadError != nil {
		return 0, d.ReadError
	}
	if len(d.Reads) == 0 {
		return 0, nil
	}
	data := d.Reads[0]
	d.Reads = d.Reads[1:]
	return copy(p, data), nil
}
func (d *Device) GetFeatureReport(p []byte) (int, error) {
	d.ReportIDs = append(d.ReportIDs, p[0])
	return d.read(p)
}
func (d *Device) GetInputReport(p []byte) (int, error) {
	d.ReportIDs = append(d.ReportIDs, p[0])
	return d.read(p)
}
func (d *Device) ReadWithTimeout(p []byte, _ time.Duration) (int, error) { return d.read(p) }
func (d *Device) GetReportDescriptor(p []byte) (int, error)              { return copy(p, d.Descriptor), nil }
func (d *Device) Close() error                                           { d.Closed++; return nil }

func Light(path, serial string) usb.Info {
	return usb.Info{Path: path, Serial: serial, VendorID: 0x046d, ProductID: 0xc900, UsagePage: 0xff43, Usage: 0x0202, Product: "Litra Glow"}
}
