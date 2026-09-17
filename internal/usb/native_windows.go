// SPDX-License-Identifier: BSD-3-Clause
package usb

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var hidDLL = windows.NewLazySystemDLL("hid.dll")
var (
	hidGUID          = hidDLL.NewProc("HidD_GetHidGuid")
	hidAttributes    = hidDLL.NewProc("HidD_GetAttributes")
	hidSerial        = hidDLL.NewProc("HidD_GetSerialNumberString")
	hidManufacturer  = hidDLL.NewProc("HidD_GetManufacturerString")
	hidProduct       = hidDLL.NewProc("HidD_GetProductString")
	hidPreparsed     = hidDLL.NewProc("HidD_GetPreparsedData")
	hidFreePreparsed = hidDLL.NewProc("HidD_FreePreparsedData")
	hidCaps          = hidDLL.NewProc("HidP_GetCaps")
	hidSetFeature    = hidDLL.NewProc("HidD_SetFeature")
	hidGetFeature    = hidDLL.NewProc("HidD_GetFeature")
	hidGetInput      = hidDLL.NewProc("HidD_GetInputReport")
)

type windowsBackend struct{}

func New() (Backend, error) {
	if err := hidDLL.Load(); err != nil {
		return nil, err
	}
	return &windowsBackend{}, nil
}
func (*windowsBackend) Close() error    { return nil }
func (*windowsBackend) Version() string { return "native Windows HID" }

type winCaps struct {
	Usage, UsagePage                                        uint16
	InputLength, OutputLength, FeatureLength                uint16
	Reserved                                                [17]uint16
	LinkCollections                                         uint16
	InputButtonCaps, InputValueCaps, InputDataIndices       uint16
	OutputButtonCaps, OutputValueCaps, OutputDataIndices    uint16
	FeatureButtonCaps, FeatureValueCaps, FeatureDataIndices uint16
}

func getWinCaps(handle windows.Handle) (winCaps, error) {
	var data uintptr
	var caps winCaps
	ok, _, err := hidPreparsed.Call(uintptr(handle), uintptr(unsafe.Pointer(&data)))
	if ok == 0 {
		return caps, fmt.Errorf("HidD_GetPreparsedData: %w", err)
	}
	defer hidFreePreparsed.Call(data)
	status, _, _ := hidCaps.Call(data, uintptr(unsafe.Pointer(&caps)))
	if uint32(status) != 0x00110000 {
		return caps, fmt.Errorf("HidP_GetCaps: status 0x%08X", uint32(status))
	}
	return caps, nil
}

func (*windowsBackend) Enumerate(vendor, product uint16) ([]Info, error) {
	var guid windows.GUID
	hidGUID.Call(uintptr(unsafe.Pointer(&guid)))
	paths, err := windows.CM_Get_Device_Interface_List("", &guid, 0)
	devices := make([]Info, 0)
	if errors.Is(err, windows.ERROR_NO_SUCH_DEVICE_INTERFACE) {
		return devices, nil
	}
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			continue
		}
		h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			continue
		}
		var attrs struct {
			Size                     uint32
			Vendor, Product, Version uint16
			Padding                  uint16
		}
		attrs.Size = uint32(unsafe.Sizeof(attrs))
		ok, _, _ := hidAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&attrs)))
		if ok == 0 || vendor != 0 && vendor != attrs.Vendor || product != 0 && product != attrs.Product {
			windows.CloseHandle(h)
			continue
		}
		caps, err := getWinCaps(h)
		if err != nil {
			windows.CloseHandle(h)
			continue
		}
		info := Info{Path: path, VendorID: attrs.Vendor, ProductID: attrs.Product, UsagePage: caps.UsagePage, Usage: caps.Usage,
			Serial: winString(h, hidSerial), Manufacturer: winString(h, hidManufacturer), Product: winString(h, hidProduct), Interface: -1, Bus: "Unknown"}
		lower := strings.ToLower(path)
		if i := strings.Index(lower, "&mi_"); i >= 0 && i+6 <= len(lower) {
			if n, err := strconv.ParseUint(lower[i+4:i+6], 16, 8); err == nil {
				info.Interface = int(n)
			}
		}
		// The path alone does not reliably distinguish USB from Bluetooth HID.
		// Leave bus unknown rather than label every HID collection as USB.
		devices = append(devices, info)
		windows.CloseHandle(h)
	}
	return devices, nil
}

func winString(h windows.Handle, proc *windows.LazyProc) string {
	buf := make([]uint16, 256)
	ok, _, _ := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2))
	if ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

func (*windowsBackend) Open(path string) (Device, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	caps, err := getWinCaps(h)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	return &winDevice{handle: h, caps: caps}, nil
}

type winDevice struct {
	handle windows.Handle
	caps   winCaps
}

func (d *winDevice) io(p []byte, write bool, timeout time.Duration) (int, error) {
	if d.handle == windows.InvalidHandle {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return 0, errors.New("empty HID buffer")
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	over := windows.Overlapped{HEvent: event}
	// Windows retains both pointers until completion/cancellation. Pin them
	// across the asynchronous call, including any Go stack growth while waiting.
	var pinned runtime.Pinner
	pinned.Pin(&over)
	pinned.Pin(&p[0])
	defer pinned.Unpin()
	var done uint32
	if write {
		err = windows.WriteFile(d.handle, p, &done, &over)
	} else {
		err = windows.ReadFile(d.handle, p, &done, &over)
	}
	if err == nil {
		return int(done), nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, err
	}
	ms := uint32(min(int64(0xfffffffe), max(0, int64((timeout+time.Millisecond-1)/time.Millisecond))))
	status, waitErr := windows.WaitForSingleObject(event, ms)
	if waitErr != nil || status != windows.WAIT_OBJECT_0 {
		// Always drain canceled I/O before Go may reclaim the buffer/OVERLAPPED.
		windows.CancelIoEx(d.handle, &over)
		windows.GetOverlappedResult(d.handle, &over, &done, true)
		runtime.KeepAlive(p)
		if waitErr != nil {
			return 0, waitErr
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			if write {
				return 0, errors.New("HID output write timed out")
			}
			return 0, nil
		}
		return 0, fmt.Errorf("unexpected HID wait status %d", status)
	}
	err = windows.GetOverlappedResult(d.handle, &over, &done, false)
	runtime.KeepAlive(p)
	return int(done), err
}

func (d *winDevice) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, errors.New("empty HID report")
	}
	buf := make([]byte, max(len(p), int(d.caps.OutputLength)))
	copy(buf, p)
	n, err := d.io(buf, true, 5*time.Second)
	if err != nil {
		return 0, err
	}
	return min(n, len(p)), nil
}
func (d *winDevice) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	buf := make([]byte, max(len(p), int(d.caps.InputLength)))
	n, err := d.io(buf, false, timeout)
	if err != nil || n == 0 {
		return n, err
	}
	if n > len(buf) {
		return 0, errors.New("invalid HID read length")
	}
	data := buf[:n]
	if data[0] == 0 {
		data = data[1:]
	}
	return copy(p, data), nil
}
func (d *winDevice) control(p []byte, proc *windows.LazyProc, length int, read bool) (int, error) {
	if d.handle == windows.InvalidHandle {
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		return 0, errors.New("empty HID report")
	}
	buf := make([]byte, max(length, len(p)))
	copy(buf, p)
	ok, _, err := proc.Call(uintptr(d.handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	runtime.KeepAlive(buf)
	if ok == 0 {
		return 0, fmt.Errorf("%s: %w", proc.Name, err)
	}
	if read {
		copy(p, buf)
	}
	return len(p), nil
}
func (d *winDevice) SendFeatureReport(p []byte) (int, error) {
	return d.control(p, hidSetFeature, int(d.caps.FeatureLength), false)
}
func (d *winDevice) GetFeatureReport(p []byte) (int, error) {
	return d.control(p, hidGetFeature, int(d.caps.FeatureLength), true)
}
func (d *winDevice) GetInputReport(p []byte) (int, error) {
	return d.control(p, hidGetInput, int(d.caps.InputLength), true)
}
func (d *winDevice) GetReportDescriptor([]byte) (int, error) {
	return 0, errors.New("Windows HID APIs expose parsed capabilities, not the original report descriptor; use macOS or Linux for --get-report-descriptor")
}
func (d *winDevice) Close() error {
	if d.handle == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(d.handle)
	d.handle = windows.InvalidHandle
	return err
}
