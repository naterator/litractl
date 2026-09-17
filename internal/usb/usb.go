// SPDX-License-Identifier: BSD-3-Clause
// Package usb provides the HID operations shared by the light controller and
// the low-level command runner. Implementations own their handles.
package usb

import (
	"errors"
	"time"
)

var ioBufferTooSmall = errors.New("HID buffer is too small for the report descriptor")

type Info struct {
	Path         string `json:"path"`
	VendorID     uint16 `json:"vendor_id"`
	ProductID    uint16 `json:"product_id"`
	Serial       string `json:"serial_number"`
	Manufacturer string `json:"manufacturer_string"`
	Product      string `json:"product_string"`
	UsagePage    uint16 `json:"usage_page"`
	Usage        uint16 `json:"usage"`
	Interface    int    `json:"interface_number"`
	BusType      int    `json:"bus_type"`
	Bus          string `json:"bus_type_name"`
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
