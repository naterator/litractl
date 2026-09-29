// SPDX-License-Identifier: BSD-3-Clause
package usb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux uses the kernel's hidraw interface and sysfs.
type linuxBackend struct{}

// sysfsHidraw is replaced by tests with a synthetic sysfs tree.
var sysfsHidraw = "/sys/class/hidraw"

func New() (Backend, error)           { return &linuxBackend{}, nil }
func (*linuxBackend) Version() string { return "native Linux hidraw" }
func (*linuxBackend) Close() error    { return nil }

func (*linuxBackend) Enumerate(vendor, product uint16) ([]Info, error) {
	paths, err := filepath.Glob(filepath.Join(sysfsHidraw, "hidraw*"))
	if err != nil {
		return nil, err
	}
	devices := make([]Info, 0)
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(path, "device/uevent"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		props := make(map[string]string)
		for _, line := range strings.Split(string(data), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				props[k] = v
			}
		}
		var bus, vid, pid uint32
		if n, _ := fmt.Sscanf(props["HID_ID"], "%x:%x:%x", &bus, &vid, &pid); n != 3 {
			continue
		}
		if vendor != 0 && uint32(vendor) != vid || product != 0 && uint32(product) != pid {
			continue
		}
		info := Info{Path: filepath.Join("/dev", filepath.Base(path)), VendorID: uint16(vid), ProductID: uint16(pid), Serial: props["HID_UNIQ"], Product: props["HID_NAME"], Interface: -1, Bus: "Unknown"}
		switch bus {
		case 3:
			info.Bus, info.BusType = "USB", 1
		case 5:
			info.Bus, info.BusType = "Bluetooth", 2
		case 0x18:
			info.Bus, info.BusType = "I2C", 3
		case 0x1c:
			info.Bus, info.BusType = "SPI", 4
		}
		parent, err := filepath.EvalSymlinks(filepath.Join(path, "device"))
		if err != nil {
			continue
		}
		for p := parent; p != "/" && p != "."; p = filepath.Dir(p) {
			read := func(name string) string {
				b, _ := os.ReadFile(filepath.Join(p, name))
				return strings.TrimSpace(string(b))
			}
			if v := read("bInterfaceNumber"); v != "" && info.Interface < 0 {
				if n, err := strconv.ParseUint(v, 16, 16); err == nil {
					info.Interface = int(n)
				}
			}
			if read("idVendor") != "" {
				info.Manufacturer = read("manufacturer")
				if v := read("product"); v != "" {
					info.Product = v
				}
				if v := read("serial"); v != "" {
					info.Serial = v
				}
				break
			}
		}
		// Like macOS, list a device whose descriptor cannot be read or parsed
		// without usages instead of failing enumeration of every device.
		var pairs []usagePair
		if descriptor, err := os.ReadFile(filepath.Join(parent, "report_descriptor")); err == nil {
			pairs, _ = descriptorUsages(descriptor)
		} else if errors.Is(err, os.ErrNotExist) {
			continue // Removed during enumeration.
		}
		if len(pairs) == 0 {
			pairs = []usagePair{{}}
		}
		for _, pair := range pairs {
			d := info
			d.UsagePage, d.Usage = pair.page, pair.usage
			devices = append(devices, d)
		}
	}
	return devices, nil
}

func (*linuxBackend) Open(path string) (Device, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrPermission) {
		// Callers already name the path, as with the other backends.
		return nil, fmt.Errorf("%w (access to /dev/hidraw requires an appropriate udev rule)", err)
	}
	if err != nil {
		return nil, err
	}
	return &linuxDevice{fd: fd}, nil
}

type linuxDevice struct{ fd int }

func (d *linuxDevice) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, errors.New("empty HID report")
	}
	return unix.Write(d.fd, p)
}

func (d *linuxDevice) report(p []byte, command uintptr) (int, error) {
	if d.fd < 0 {
		return 0, os.ErrClosed
	}
	if len(p) == 0 || len(p) > 4096 {
		return 0, errors.New("HID report length must be 1..4096")
	}
	// _IOC(_IOC_READ|_IOC_WRITE, 'H', command, length), from linux/hidraw.h.
	request := uintptr(3<<30) | uintptr(len(p))<<16 | uintptr('H')<<8 | command
	n, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), request, uintptr(unsafe.Pointer(&p[0])))
	runtime.KeepAlive(p)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}
func (d *linuxDevice) SendFeatureReport(p []byte) (int, error) { return d.report(p, 0x06) }
func (d *linuxDevice) GetFeatureReport(p []byte) (int, error)  { return d.report(p, 0x07) }
func (d *linuxDevice) GetInputReport(p []byte) (int, error)    { return d.report(p, 0x0a) }

func (d *linuxDevice) GetReportDescriptor(p []byte) (int, error) {
	if d.fd < 0 {
		return 0, os.ErrClosed
	}
	// Request numbers come from x/sys because their encoding varies by
	// architecture (for example ppc64le and mips64).
	var descriptor unix.HIDRawReportDescriptor
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), unix.HIDIOCGRDESCSIZE, uintptr(unsafe.Pointer(&descriptor.Size)))
	if errno != 0 {
		return 0, errno
	}
	if descriptor.Size > 4096 || int(descriptor.Size) > len(p) {
		return 0, errBufferTooSmall
	}
	if err := unix.IoctlHIDGetDesc(d.fd, &descriptor); err != nil {
		return 0, err
	}
	if descriptor.Size > 4096 || int(descriptor.Size) > len(p) {
		return 0, errBufferTooSmall
	}
	return copy(p, descriptor.Value[:descriptor.Size]), nil
}

func (d *linuxDevice) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	if d.fd < 0 {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return 0, errors.New("empty HID buffer")
	}
	deadline := time.Now().Add(timeout)
	for {
		ms := int(max(0, time.Until(deadline)+time.Millisecond-1) / time.Millisecond)
		fds := []unix.PollFd{{Fd: int32(d.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, ms)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, nil
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return 0, errors.New("HID device disconnected or unavailable")
		}
		n, err = unix.Read(d.fd, p)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			if !time.Now().Before(deadline) {
				return 0, nil
			}
			continue
		}
		return n, err
	}
}
func (d *linuxDevice) Close() error {
	if d.fd < 0 {
		return nil
	}
	err := unix.Close(d.fd)
	d.fd = -1
	return err
}
