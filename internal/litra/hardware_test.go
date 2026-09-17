//go:build integration

// SPDX-License-Identifier: BSD-3-Clause
package litra

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/naterator/litractl/internal/usb"
)

// Opt in with go test -tags=integration -run TestHardware -v ./internal/litra.
// This test briefly changes ALL attached Litra Glow lights, then restores the
// exact raw brightness, temperature and power values captured from each light.
func TestHardware(t *testing.T) {
	b, err := usb.New()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	devices, err := Devices(b, Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) == 0 {
		t.Fatal("no Litra Glow lights attached")
	}
	type state struct{ power, brightness, temperature Action }
	before := make(map[string]state)
	query := func(info usb.Info, fn byte) ([]byte, error) {
		d, err := b.Open(info.Path)
		if err != nil {
			return nil, err
		}
		defer d.Close()
		report := [ReportSize]byte{0x11, 0xff, 4, fn}
		if n, err := d.Write(report[:]); err != nil {
			return nil, err
		} else if n != ReportSize {
			return nil, io.ErrShortWrite
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			buf := make([]byte, ReportSize)
			n, err := d.ReadWithTimeout(buf, 100*time.Millisecond)
			if err != nil {
				return nil, err
			}
			if n == ReportSize && buf[0] == 0x11 && buf[1] == 0xff && buf[2] == 4 && buf[3] == fn {
				return buf, nil
			}
		}
		return nil, fmt.Errorf("no response to function %02x", fn)
	}
	for _, info := range devices {
		var saved state
		for _, item := range []struct {
			fn, write byte
			out       *Action
		}{{0x0c, 0x1c, &saved.power}, {0x3c, 0x4c, &saved.brightness}, {0x8c, 0x9c, &saved.temperature}} {
			buf, err := query(info, item.fn)
			if err != nil {
				t.Fatal(err)
			}
			copy(item.out.Report[:], buf)
			item.out.Report[3] = item.write
		}
		before[info.Path] = saved
		t.Logf("%s initial power=%d brightness_raw=%d temperature=%dK", info.Serial, saved.power.Report[4], saved.brightness.Report[5], int(saved.temperature.Report[4])*256+int(saved.temperature.Report[5]))
	}
	defer func() {
		for _, info := range devices {
			s := before[info.Path]
			if err := Apply(context.Background(), b, []usb.Info{info}, []Action{s.brightness, s.temperature, s.power}, io.Discard); err != nil {
				t.Errorf("RESTORE %s: %v", info.Serial, err)
				continue
			}
			for _, item := range []struct {
				fn   byte
				want Action
			}{{0x0c, s.power}, {0x3c, s.brightness}, {0x8c, s.temperature}} {
				got, err := query(info, item.fn)
				if err != nil || got[4] != item.want.Report[4] || got[5] != item.want.Report[5] {
					t.Errorf("restore verification %s function %02x: %x %v", info.Serial, item.fn, got, err)
				}
			}
			t.Logf("%s restored original settings", info.Serial)
		}
	}()
	for _, commands := range [][]string{{"off"}, {"on", "brightness", "65", "temperature", "4500"}, {"normal", "warm"}} {
		actions, err := Parse(commands)
		if err != nil {
			t.Fatal(err)
		}
		if err := Apply(context.Background(), b, devices, actions, io.Discard); err != nil {
			t.Fatal(err)
		}
		for _, info := range devices {
			for _, a := range actions {
				fn := a.Report[3] - 0x10
				got, err := query(info, fn)
				if err != nil {
					t.Fatal(err)
				}
				if got[4] != a.Report[4] || got[5] != a.Report[5] {
					t.Fatalf("%s %s: returned %x, want %x", info.Serial, a.Description, got[4:6], a.Report[4:6])
				}
				t.Logf("%s verified %s", info.Serial, a.Description)
			}
		}
	}
}
