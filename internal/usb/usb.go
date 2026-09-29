// SPDX-License-Identifier: BSD-3-Clause
// Package usb provides the HID operations shared by the light controller and
// the low-level command runner. Implementations own their handles.
package usb

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var errBufferTooSmall = errors.New("HID buffer is too small for the report descriptor")

type Info struct {
	Path         string
	VendorID     uint16
	ProductID    uint16
	Serial       string
	Manufacturer string
	Product      string
	UsagePage    uint16
	Usage        uint16
	Interface    int
	BusType      int
	Bus          string
}

// MarshalJSON writes identifiers as hexadecimal strings, the way HID IDs are
// conventionally shown. list --json and hid --list-json share this format.
func (d Info) MarshalJSON() ([]byte, error) {
	id := func(n uint16) string { return fmt.Sprintf("0x%04X", n) }
	return json.Marshal(struct {
		Path         string `json:"path"`
		VendorID     string `json:"vendor_id"`
		ProductID    string `json:"product_id"`
		Serial       string `json:"serial_number"`
		Manufacturer string `json:"manufacturer_string"`
		Product      string `json:"product_string"`
		UsagePage    string `json:"usage_page"`
		Usage        string `json:"usage"`
		Interface    int    `json:"interface_number"`
		BusType      int    `json:"bus_type"`
		Bus          string `json:"bus_type_name"`
	}{d.Path, id(d.VendorID), id(d.ProductID), d.Serial, d.Manufacturer, d.Product,
		id(d.UsagePage), id(d.Usage), d.Interface, d.BusType, d.Bus})
}

type Device interface {
	Write([]byte) (int, error)
	SendFeatureReport([]byte) (int, error)
	GetFeatureReport([]byte) (int, error)
	GetInputReport([]byte) (int, error)
	ReadWithTimeout([]byte, time.Duration) (int, error)
	GetReportDescriptor([]byte) (int, error)
	Close() error
}

type Backend interface {
	Enumerate(vendor, product uint16) ([]Info, error)
	Open(path string) (Device, error)
	Version() string
	Close() error
}

type Factory func() (Backend, error)
