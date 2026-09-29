// SPDX-License-Identifier: BSD-3-Clause
package usb

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeHidraw adds /sys/class/hidraw/NAME whose device link points at a HID
// device below a USB interface and device, as the kernel lays them out.
func fakeHidraw(t *testing.T, root, name, usbDevice, uevent string, descriptor []byte) {
	t.Helper()
	device := filepath.Join(root, "devices", usbDevice)
	iface := filepath.Join(device, usbDevice+":1.0")
	hid := filepath.Join(iface, "0003:"+name)
	files := map[string]string{
		filepath.Join(hid, "uevent"):             uevent,
		filepath.Join(iface, "bInterfaceNumber"): "00\n",
		filepath.Join(device, "idVendor"):        "046d\n",
		filepath.Join(device, "manufacturer"):    "Logitech\n",
		filepath.Join(device, "product"):         "Litra Glow\n",
		filepath.Join(device, "serial"):          "SERIAL-" + name + "\n",
	}
	if err := os.MkdirAll(filepath.Join(root, "class", "hidraw", name), 0o755); err != nil {
		t.Fatal(err)
	}
	if descriptor != nil {
		files[filepath.Join(hid, "report_descriptor")] = string(descriptor)
	}
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(hid, filepath.Join(root, "class", "hidraw", name, "device")); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxEnumerateToleratesBadDescriptors(t *testing.T) {
	root := t.TempDir()
	litra, _ := hex.DecodeString(litraDescriptorHex)
	fakeHidraw(t, root, "hidraw0", "1-1", "HID_ID=0003:0000046D:0000C900\nHID_NAME=Litra\n", litra)
	// An unrelated device with a malformed descriptor must not hide the light.
	fakeHidraw(t, root, "hidraw1", "1-2", "HID_ID=0003:00001234:00005678\n", []byte{0xc0})
	// A device unplugged during enumeration is skipped.
	fakeHidraw(t, root, "hidraw2", "1-3", "HID_ID=0003:0000046D:0000C900\n", nil)
	old := sysfsHidraw
	sysfsHidraw = filepath.Join(root, "class", "hidraw")
	t.Cleanup(func() { sysfsHidraw = old })

	all, err := (&linuxBackend{}).Enumerate(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		path, serial string
		page, usage  uint16
	}
	var got []row
	for _, d := range all {
		got = append(got, row{d.Path, d.Serial, d.UsagePage, d.Usage})
	}
	want := []row{
		{"/dev/hidraw0", "SERIAL-hidraw0", 0x0c, 1}, {"/dev/hidraw0", "SERIAL-hidraw0", 0xff43, 0x202},
		{"/dev/hidraw1", "SERIAL-hidraw1", 0, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if d := all[0]; d.Manufacturer != "Logitech" || d.Product != "Litra Glow" || d.Interface != 0 || d.Bus != "USB" {
		t.Fatalf("device details %+v", d)
	}

	lights, err := (&linuxBackend{}).Enumerate(0x046d, 0xc900)
	if err != nil || len(lights) != 2 || lights[0].Path != "/dev/hidraw0" {
		t.Fatalf("filtered = %+v, %v", lights, err)
	}
}
