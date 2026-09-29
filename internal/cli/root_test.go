// SPDX-License-Identifier: BSD-3-Clause
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/naterator/litractl/internal/update"
	"github.com/naterator/litractl/internal/usb"
	"github.com/naterator/litractl/internal/usbtest"
)

func TestCLIChainsAndSelectors(t *testing.T) {
	d := &usbtest.Device{}
	b := &usbtest.Backend{Infos: []usb.Info{usbtest.Light("a", "one"), usbtest.Light("b", "two")}, Devices: map[string]*usbtest.Device{"b": d}}
	cmd := newCommand("test", func() (usb.Backend, error) { return b, nil }, nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"on", "normal", "warm", "brightness", "65", "color", "4500", "--serial", "two"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(d.Writes) != 5 || b.Closed != 1 || len(b.Opened) != 1 || b.Opened[0] != "b" {
		t.Fatalf("bad execution: %+v %+v", d, b)
	}
}

func run(t *testing.T, b *usbtest.Backend, args ...string) string {
	t.Helper()
	cmd := newCommand("test", func() (usb.Backend, error) { return b, nil }, nil)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestListingFormats(t *testing.T) {
	unnamed := usbtest.Light("/dev/hidraw1", "")
	b := &usbtest.Backend{Infos: []usb.Info{usbtest.Light("/dev/hidraw0", "one"), unnamed},
		Devices: map[string]*usbtest.Device{"/dev/hidraw1": {}}}

	var list []map[string]any
	if err := json.Unmarshal([]byte(run(t, b, "list", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	var hid struct{ Devices []map[string]any }
	if err := json.Unmarshal([]byte(run(t, b, "hid", "--usagePage", "0xff43", "--list-json")), &hid); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !reflect.DeepEqual(list, hid.Devices) || list[0]["vendor_id"] != "0x046D" || list[0]["usage"] != "0x0202" {
		t.Fatalf("list --json %v\nhid --list-json %v", list, hid.Devices)
	}

	if text := run(t, b, "list"); !strings.Contains(text, "(no serial)  Litra Glow\n  path: /dev/hidraw1") {
		t.Fatalf("list output %q", text)
	}
	if text := run(t, b, "--path", "/dev/hidraw1", "on"); text != "/dev/hidraw1: on\n" {
		t.Fatalf("light without serial reported as %q", text)
	}
}

func TestNoHardwareForHelpDryRunAndInvalidCommands(t *testing.T) {
	for _, tt := range []struct {
		args []string
		fail bool
	}{
		{nil, false}, {[]string{"--help"}, false}, {[]string{"hid", "--help"}, false}, {[]string{"version"}, false},
		{[]string{"--dry-run", "on", "normal", "warm"}, false}, {[]string{"--dry-run", "set_brightness", "10"}, false},
		{[]string{"on", "normal", "typo"}, true}, {[]string{"brightness", "101"}, true},
		{[]string{"hid", "--open", "--send-output", "1", "--bogus"}, true},
		{[]string{"--dry-run", "update"}, true},
		{[]string{"--dry-run", "hid", "--open-path", "a", "--send-output", "1"}, true},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := newCommand("test", func() (usb.Backend, error) { t.Fatal("unexpected hardware access"); return nil, nil }, nil)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if (err != nil) != tt.fail {
				t.Fatalf("err=%v output=%s", err, out.String())
			}
		})
	}
}

func TestUpdateModes(t *testing.T) {
	for _, args := range [][]string{{"update"}, {"update", "--check"}, {"update", "check"}} {
		called := false
		cmd := newCommand("v1.0.0", nil, func(_ context.Context, current string, check bool) (update.Result, error) {
			called = true
			if check != (len(args) > 1) || current != "v1.0.0" {
				t.Fatalf("update args %v %v", current, check)
			}
			return update.Result{Current: current, Latest: "v2.0.0", Available: true}, nil
		})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil || !called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	}
}
