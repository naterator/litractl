// SPDX-License-Identifier: BSD-3-Clause
package usb

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Calls use Apple's system frameworks. Signatures follow the macOS SDK.
var mac struct {
	once                            sync.Once
	err                             error
	managerCreate                   func(uintptr, uint32) uintptr
	managerMatching                 func(uintptr, uintptr)
	managerDevices                  func(uintptr) uintptr
	deviceCreate                    func(uintptr, uint32) uintptr
	deviceOpen                      func(uintptr, uint32) int32
	deviceClose                     func(uintptr, uint32) int32
	deviceProperty                  func(uintptr, uintptr) uintptr
	deviceService                   func(uintptr) uint32
	entryID                         func(uint32, *uint64) int32
	entryMatching                   func(uint64) uintptr
	matchingService                 func(uint32, uintptr) uint32
	objectRelease                   func(uint32) int32
	searchProperty                  func(uint32, string, uintptr, uintptr, uint32) uintptr
	setReport                       func(uintptr, uint32, uintptr, *byte, int) int32
	getReport                       func(uintptr, uint32, uintptr, *byte, *int) int32
	inputCallback                   func(uintptr, *byte, int, uintptr, uintptr)
	removalCallback                 func(uintptr, uintptr, uintptr)
	schedule                        func(uintptr, uintptr, uintptr)
	unschedule                      func(uintptr, uintptr, uintptr)
	release                         func(uintptr)
	setCount                        func(uintptr) int
	setValues                       func(uintptr, *uintptr)
	stringCreate                    func(uintptr, string, uint32) uintptr
	stringGet                       func(uintptr, *byte, int, uint32) bool
	numberGet                       func(uintptr, int32, *int32) bool
	dataLength                      func(uintptr) int
	dataBytes                       func(uintptr) *byte
	getType                         func(uintptr) uintptr
	numberType                      func() uintptr
	stringType                      func() uintptr
	dataType                        func() uintptr
	runLoop                         func() uintptr
	runMode                         func(uintptr, float64, bool) int32
	reportCallback, removedCallback uintptr
}

var macDevices struct {
	sync.Mutex
	next    uintptr
	devices map[uintptr]*macDevice
}

func loadMac() {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		mac.err = err
		return
	}
	iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		mac.err = err
		return
	}
	// Framework handles live for the process lifetime, like the function pointers.
	for name, target := range map[string]any{
		"IOHIDManagerCreate": &mac.managerCreate, "IOHIDManagerSetDeviceMatching": &mac.managerMatching,
		"IOHIDManagerCopyDevices": &mac.managerDevices, "IOHIDDeviceCreate": &mac.deviceCreate,
		"IOHIDDeviceOpen": &mac.deviceOpen, "IOHIDDeviceClose": &mac.deviceClose,
		"IOHIDDeviceGetProperty": &mac.deviceProperty, "IOHIDDeviceGetService": &mac.deviceService,
		"IORegistryEntryGetRegistryEntryID": &mac.entryID, "IORegistryEntryIDMatching": &mac.entryMatching,
		"IOServiceGetMatchingService": &mac.matchingService, "IOObjectRelease": &mac.objectRelease,
		"IORegistryEntrySearchCFProperty": &mac.searchProperty,
		"IOHIDDeviceSetReport":            &mac.setReport, "IOHIDDeviceGetReport": &mac.getReport,
		"IOHIDDeviceRegisterInputReportCallback": &mac.inputCallback, "IOHIDDeviceRegisterRemovalCallback": &mac.removalCallback,
		"IOHIDDeviceScheduleWithRunLoop": &mac.schedule, "IOHIDDeviceUnscheduleFromRunLoop": &mac.unschedule,
	} {
		if err := registerMac(target, iokit, name); err != nil {
			mac.err = err
			return
		}
	}
	for name, target := range map[string]any{
		"CFRelease": &mac.release, "CFSetGetCount": &mac.setCount, "CFSetGetValues": &mac.setValues,
		"CFStringCreateWithCString": &mac.stringCreate, "CFStringGetCString": &mac.stringGet,
		"CFNumberGetValue": &mac.numberGet, "CFDataGetLength": &mac.dataLength, "CFDataGetBytePtr": &mac.dataBytes,
		"CFGetTypeID": &mac.getType, "CFNumberGetTypeID": &mac.numberType,
		"CFStringGetTypeID": &mac.stringType, "CFDataGetTypeID": &mac.dataType,
		"CFRunLoopGetCurrent": &mac.runLoop, "CFRunLoopRunInMode": &mac.runMode,
	} {
		if err := registerMac(target, cf, name); err != nil {
			mac.err = err
			return
		}
	}
	macDevices.devices = make(map[uintptr]*macDevice)
	mac.reportCallback = purego.NewCallback(func(token uintptr, result int32, _ uintptr, _ uint32, _ uint32, report *byte, length int) {
		macDevices.Lock()
		d := macDevices.devices[token]
		macDevices.Unlock()
		if d == nil {
			return
		}
		if result != 0 {
			d.readErr = macError("input report", result)
			return
		}
		if report == nil || length <= 0 || length > len(d.buffer) {
			return
		}
		data := append([]byte(nil), unsafe.Slice(report, length)...)
		if len(d.queue) == 32 {
			d.queue = d.queue[1:]
		}
		d.queue = append(d.queue, data)
	})
	mac.removedCallback = purego.NewCallback(func(token uintptr, _ int32, _ uintptr) {
		macDevices.Lock()
		d := macDevices.devices[token]
		macDevices.Unlock()
		if d != nil {
			d.readErr = errors.New("HID device disconnected")
		}
	})
}

func registerMac(target any, library uintptr, name string) error {
	symbol, err := purego.Dlsym(library, name)
	if err != nil {
		return err
	}
	purego.RegisterFunc(target, symbol)
	return nil
}

type macBackend struct {
	manager, loop, mode uintptr
	devices             map[*macDevice]bool
}

// New and all methods including Close must be called on the same goroutine.
// Locking it to an OS thread keeps IOHID callbacks and their run loop together.
func New() (Backend, error) {
	mac.once.Do(loadMac)
	if mac.err != nil {
		return nil, mac.err
	}
	runtime.LockOSThread()
	m := mac.managerCreate(0, 0)
	if m == 0 {
		runtime.UnlockOSThread()
		return nil, errors.New("IOHIDManagerCreate failed")
	}
	mac.managerMatching(m, 0)
	return &macBackend{manager: m, loop: mac.runLoop(), mode: mac.stringCreate(0, "litracli.HID", 0x08000100), devices: make(map[*macDevice]bool)}, nil
}

func (b *macBackend) Version() string { return "native macOS IOKit" }
func (b *macBackend) Close() error {
	if b.manager == 0 {
		return nil
	}
	var errs []error
	for d := range b.devices {
		errs = append(errs, d.Close())
	}
	mac.release(b.manager)
	mac.release(b.mode)
	b.manager = 0
	runtime.UnlockOSThread()
	return errors.Join(errs...)
}

func (b *macBackend) Enumerate(vendor, product uint16) ([]Info, error) {
	devices := make([]Info, 0)
	if b.manager == 0 {
		return nil, errors.New("HID backend is closed")
	}
	set := mac.managerDevices(b.manager)
	if set == 0 {
		return devices, nil
	}
	defer mac.release(set)
	count := mac.setCount(set)
	if count <= 0 {
		return devices, nil
	}
	refs := make([]uintptr, count)
	mac.setValues(set, &refs[0])
	for _, ref := range refs {
		vid, pid := uint16(macInt(ref, "VendorID")), uint16(macInt(ref, "ProductID"))
		if vendor != 0 && vendor != vid || product != 0 && product != pid {
			continue
		}
		var id uint64
		service := mac.deviceService(ref)
		if service == 0 || mac.entryID(service, &id) != 0 {
			continue
		}
		info := Info{Path: fmt.Sprintf("DevSrvsID:%d", id), VendorID: vid, ProductID: pid,
			Serial: macString(ref, "SerialNumber"), Manufacturer: macString(ref, "Manufacturer"), Product: macString(ref, "Product"), Interface: -1,
			UsagePage: uint16(macInt(ref, "PrimaryUsagePage")), Usage: uint16(macInt(ref, "PrimaryUsage")),
		}
		transport := macString(ref, "Transport")
		switch {
		case transport == "USB":
			info.BusType, info.Bus = 1, "USB"
		case strings.HasPrefix(transport, "Bluetooth"):
			info.BusType, info.Bus = 2, "Bluetooth"
		case transport == "I2C":
			info.BusType, info.Bus = 3, "I2C"
		case transport == "SPI":
			info.BusType, info.Bus = 4, "SPI"
		default:
			info.Bus = "Unknown"
		}
		key := mac.stringCreate(0, "bInterfaceNumber", 0x08000100)
		prop := mac.searchProperty(service, "IOService", key, 0, 3)
		mac.release(key)
		if prop != 0 {
			var n int32
			if mac.getType(prop) == mac.numberType() && mac.numberGet(prop, 3, &n) {
				info.Interface = int(n)
			}
			mac.release(prop)
		}
		pairs, err := descriptorUsages(macData(ref, "ReportDescriptor"))
		if err != nil || len(pairs) == 0 {
			pairs = []usagePair{{info.UsagePage, info.Usage}}
		}
		seen := make(map[usagePair]bool)
		for _, pair := range pairs {
			if !seen[pair] {
				seen[pair] = true
				row := info
				row.UsagePage, row.Usage = pair.page, pair.usage
				devices = append(devices, row)
			}
		}
	}
	sort.SliceStable(devices, func(i, j int) bool { return devices[i].Path < devices[j].Path })
	return devices, nil
}

func macProperty(ref uintptr, name string) uintptr {
	key := mac.stringCreate(0, name, 0x08000100)
	defer mac.release(key)
	return mac.deviceProperty(ref, key)
}
func macInt(ref uintptr, name string) int32 {
	prop := macProperty(ref, name)
	var n int32
	if prop != 0 && mac.getType(prop) == mac.numberType() {
		mac.numberGet(prop, 3, &n)
	}
	return n
}
func macString(ref uintptr, name string) string {
	prop := macProperty(ref, name)
	if prop == 0 || mac.getType(prop) != mac.stringType() {
		return ""
	}
	buf := make([]byte, 4096)
	if !mac.stringGet(prop, &buf[0], len(buf), 0x08000100) {
		return ""
	}
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i])
		}
	}
	return ""
}
func macData(ref uintptr, name string) []byte {
	prop := macProperty(ref, name)
	if prop == 0 || mac.getType(prop) != mac.dataType() {
		return nil
	}
	n := mac.dataLength(prop)
	if n <= 0 || n > 1<<20 {
		return nil
	}
	return append([]byte(nil), unsafe.Slice(mac.dataBytes(prop), n)...)
}

type macDevice struct {
	b          *macBackend
	ref, token uintptr
	buffer     []byte
	pinner     runtime.Pinner
	queue      [][]byte
	readErr    error
}

func (b *macBackend) Open(path string) (Device, error) {
	if b.manager == 0 {
		return nil, errors.New("HID backend is closed")
	}
	if !strings.HasPrefix(path, "DevSrvsID:") {
		return nil, errors.New("macOS HID paths must be DevSrvsID:<registry ID>; use list to find one")
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(path, "DevSrvsID:"), 10, 64)
	if err != nil {
		return nil, err
	}
	entry := mac.matchingService(0, mac.entryMatching(id))
	if entry == 0 {
		return nil, errors.New("HID device no longer exists")
	}
	defer mac.objectRelease(entry)
	ref := mac.deviceCreate(0, entry)
	if ref == 0 {
		return nil, errors.New("IOHIDDeviceCreate failed")
	}
	if err := macError("IOHIDDeviceOpen", mac.deviceOpen(ref, 0)); err != nil {
		mac.release(ref)
		return nil, err
	}
	d := &macDevice{b: b, ref: ref}
	size := int(macInt(ref, "MaxInputReportSize"))
	if size <= 0 {
		size = 4096
	}
	if size > 1<<20 {
		mac.deviceClose(ref, 0)
		mac.release(ref)
		return nil, errors.New("HID input report size exceeds 1 MiB")
	}
	d.buffer = make([]byte, size)
	d.pinner.Pin(&d.buffer[0])
	macDevices.Lock()
	macDevices.next++
	d.token = macDevices.next
	macDevices.devices[d.token] = d
	macDevices.Unlock()
	mac.inputCallback(ref, &d.buffer[0], size, mac.reportCallback, d.token)
	mac.removalCallback(ref, mac.removedCallback, d.token)
	mac.schedule(ref, b.loop, b.mode)
	b.devices[d] = true
	return d, nil
}

func macError(op string, result int32) error {
	if result == 0 {
		return nil
	}
	if uint32(result) == 0xe00002e2 {
		return fmt.Errorf("%s: USB access is not permitted (IOReturn 0xE00002E2); run outside the application sandbox", op)
	}
	return fmt.Errorf("%s failed (IOReturn 0x%08X)", op, uint32(result))
}
func (d *macDevice) ready(p []byte) error {
	if d.ref == 0 {
		return errors.New("HID device is closed")
	}
	if d.readErr != nil {
		return d.readErr
	}
	if len(p) == 0 {
		return errors.New("empty HID buffer")
	}
	return nil
}
func (d *macDevice) set(p []byte, kind uint32) (int, error) {
	if err := d.ready(p); err != nil {
		return 0, err
	}
	data := p
	if p[0] == 0 {
		data = p[1:]
	}
	if len(data) == 0 {
		return 0, errors.New("report needs payload bytes")
	}
	err := macError("IOHIDDeviceSetReport", mac.setReport(d.ref, kind, uintptr(p[0]), &data[0], len(data)))
	runtime.KeepAlive(p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
func (d *macDevice) get(p []byte, kind uint32) (int, error) {
	if err := d.ready(p); err != nil {
		return 0, err
	}
	data := p
	prefix := 0
	if p[0] == 0 {
		data = p[1:]
		prefix = 1
	}
	if len(data) == 0 {
		return 0, errors.New("report needs payload bytes")
	}
	n := len(data)
	err := macError("IOHIDDeviceGetReport", mac.getReport(d.ref, kind, uintptr(p[0]), &data[0], &n))
	runtime.KeepAlive(p)
	if err != nil {
		return 0, err
	}
	return n + prefix, nil
}
func (d *macDevice) Write(p []byte) (int, error)             { return d.set(p, 1) }
func (d *macDevice) SendFeatureReport(p []byte) (int, error) { return d.set(p, 2) }
func (d *macDevice) GetFeatureReport(p []byte) (int, error)  { return d.get(p, 2) }
func (d *macDevice) GetInputReport(p []byte) (int, error)    { return d.get(p, 0) }
func (d *macDevice) GetReportDescriptor(p []byte) (int, error) {
	if err := d.ready(p); err != nil {
		return 0, err
	}
	data := macData(d.ref, "ReportDescriptor")
	if len(data) == 0 {
		return 0, errors.New("device has no report descriptor")
	}
	if len(data) > len(p) {
		return 0, ioBufferTooSmall
	}
	return copy(p, data), nil
}
func (d *macDevice) ReadWithTimeout(p []byte, timeout time.Duration) (int, error) {
	if err := d.ready(p); err != nil {
		return 0, err
	}
	deadline := time.Now().Add(timeout)
	for {
		if len(d.queue) != 0 {
			data := d.queue[0]
			d.queue = d.queue[1:]
			return copy(p, data), nil
		}
		if d.readErr != nil {
			return 0, d.readErr
		}
		seconds := max(0, time.Until(deadline).Seconds())
		mac.runMode(d.b.mode, seconds, true)
		if len(d.queue) == 0 && !time.Now().Before(deadline) {
			return 0, d.readErr
		}
	}
}
func (d *macDevice) Close() error {
	if d.ref == 0 {
		return nil
	}
	mac.unschedule(d.ref, d.b.loop, d.b.mode)
	mac.inputCallback(d.ref, &d.buffer[0], len(d.buffer), 0, 0)
	mac.removalCallback(d.ref, 0, 0)
	err := macError("IOHIDDeviceClose", mac.deviceClose(d.ref, 0))
	mac.release(d.ref)
	d.ref = 0
	d.pinner.Unpin()
	macDevices.Lock()
	delete(macDevices.devices, d.token)
	macDevices.Unlock()
	delete(d.b.devices, d)
	return err
}
