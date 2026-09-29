// SPDX-License-Identifier: BSD-3-Clause
package litra

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/naterator/litractl/internal/usb"
	"github.com/naterator/litractl/internal/usbtest"
)

func TestPresetReports(t *testing.T) {
	cases := []struct {
		command string
		prefix  []byte
	}{
		{"on", []byte{0x11, 0xff, 4, 0x1c, 1}}, {"off", []byte{0x11, 0xff, 4, 0x1c, 0}},
		{"glow", []byte{0x11, 0xff, 4, 0x4c, 0, 43}}, {"dim", []byte{0x11, 0xff, 4, 0x4c, 0, 66}},
		{"normal", []byte{0x11, 0xff, 4, 0x4c, 0, 112}}, {"medium", []byte{0x11, 0xff, 4, 0x4c, 0, 158}},
		{"bright", []byte{0x11, 0xff, 4, 0x4c, 0, 204}}, {"brightest", []byte{0x11, 0xff, 4, 0x4c, 0, 250}},
		{"warmest", []byte{0x11, 0xff, 4, 0x9c, 0x0a, 0x8c}}, {"warm", []byte{0x11, 0xff, 4, 0x9c, 0x0b, 0xb8}},
		{"mild", []byte{0x11, 0xff, 4, 0x9c, 0x0d, 0xac}}, {"neutral", []byte{0x11, 0xff, 4, 0x9c, 0x0f, 0xa0}},
		{"cool", []byte{0x11, 0xff, 4, 0x9c, 0x13, 0x88}}, {"cold", []byte{0x11, 0xff, 4, 0x9c, 0x15, 0x7c}},
		{"coldest", []byte{0x11, 0xff, 4, 0x9c, 0x19, 0x64}},
	}
	for _, tt := range cases {
		t.Run(tt.command, func(t *testing.T) {
			a, err := Parse([]string{tt.command})
			if err != nil {
				t.Fatal(err)
			}
			want := make([]byte, 20)
			copy(want, tt.prefix)
			if !bytes.Equal(a[0].Report[:], want) {
				t.Fatalf("got % X, want % X", a[0].Report, want)
			}
		})
	}
}

func TestParseNumericAndInvalidSequences(t *testing.T) {
	a, err := Parse([]string{"on", "brightness", "0%", "color", "6500K", "set_brightness", "33", "set_temperature", "2700", "off"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 6 || a[1].Report[5] != 20 || a[3].Report[5] != 95 {
		t.Fatalf("bad numeric reports: %+v", a)
	}
	for _, args := range [][]string{nil, {"on", "typo"}, {"brightness"}, {"brightness", "101"}, {"brightness", "-1"}, {"brightness", "NaN"}, {"brightness", "1.5"}, {"temperature", "2699"}, {"temperature", "6501"}, {"temperature", "2700%"}} {
		if _, err := Parse(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestDeviceSelectionAndDeduplication(t *testing.T) {
	a, b := usbtest.Light("a", "one"), usbtest.Light("b", "two")
	consumer := a
	consumer.UsagePage = 0x0c
	consumer.Usage = 1
	unrelated := a
	unrelated.VendorID = 0xffff
	f := &usbtest.Backend{Infos: []usb.Info{consumer, b, a, a, unrelated}}
	for _, tt := range []struct {
		selector Selector
		paths    []string
	}{{Selector{}, []string{"a", "b"}}, {Selector{Serial: "two"}, []string{"b"}}, {Selector{Path: "a"}, []string{"a"}}, {Selector{Serial: "one", Path: "b"}, []string{}}} {
		got, err := Devices(f, tt.selector)
		if err != nil {
			t.Fatal(err)
		}
		paths := []string{}
		for _, d := range got {
			paths = append(paths, d.Path)
		}
		if !reflect.DeepEqual(paths, tt.paths) {
			t.Fatalf("selection %+v: %v", tt.selector, paths)
		}
	}
}

func TestAllLightsAndPartialFailures(t *testing.T) {
	for _, failure := range []string{"", "open", "write", "short"} {
		t.Run(failure, func(t *testing.T) {
			a, b := &usbtest.Device{}, &usbtest.Device{}
			backend := &usbtest.Backend{Devices: map[string]*usbtest.Device{"a": a, "b": b}, OpenErrors: map[string]error{}}
			switch failure {
			case "open":
				backend.OpenErrors["a"] = errors.New("unplugged")
			case "write":
				a.WriteError = errors.New("disconnected")
			case "short":
				a.ShortWrite = true
			}
			actions, _ := Parse([]string{"on", "normal", "warm"})
			err := Apply(context.Background(), backend, []usb.Info{usbtest.Light("a", "one"), usbtest.Light("b", "two")}, actions, io.Discard)
			if (err != nil) != (failure != "") {
				t.Fatalf("error = %v", err)
			}
			if len(b.Writes) != 3 || b.Closed != 1 {
				t.Fatalf("second light not completed/closed: %+v", b)
			}
			if failure != "open" && a.Closed != 1 {
				t.Fatal("first light not closed")
			}
			for i, w := range b.Writes {
				if !bytes.Equal(w, actions[i].Report[:]) {
					t.Fatal("report order changed")
				}
			}
		})
	}
	if Apply(context.Background(), &usbtest.Backend{}, nil, nil, io.Discard) == nil {
		t.Fatal("missing lights accepted")
	}
}

func TestApplyCanceledBeforeOpening(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &usbtest.Backend{}
	actions, _ := Parse([]string{"on"})
	err := Apply(ctx, backend, []usb.Info{usbtest.Light("a", "one")}, actions, io.Discard)
	if !errors.Is(err, context.Canceled) || strings.Count(err.Error(), context.Canceled.Error()) != 1 {
		t.Fatalf("error = %q", err)
	}
	if len(backend.Opened) != 0 {
		t.Fatalf("opened %v after cancellation", backend.Opened)
	}
}
