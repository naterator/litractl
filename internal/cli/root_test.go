// SPDX-License-Identifier: BSD-3-Clause
package cli

import (
	"bytes"
	"context"
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
