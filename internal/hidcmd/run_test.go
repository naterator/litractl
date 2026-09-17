// SPDX-License-Identifier: BSD-3-Clause
package hidcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/naterator/litractl/internal/usb"
	"github.com/naterator/litractl/internal/usbtest"
)

func TestOrderedOperations(t *testing.T) {
	d := &usbtest.Device{Reads: [][]byte{{3, 99}, {4, 88}, {17, 77}}, Descriptor: []byte{5, 12}}
	b := &usbtest.Backend{Infos: []usb.Info{usbtest.Light("a", "one"), usbtest.Light("b", "two")}, Devices: map[string]*usbtest.Device{"b": d}}
	p, err := Parse([]string{"--vidpid", "046D/C900", "--serial", "two", "--usagePage", "FF43", "--usage", "0x202", "--open", "-l4", "--send-out", "17,0xff", "-l", "3", "--send-feature", "3,0x63,0", "--read-feature", "3", "--read-input-report", "4", "--read-in", "--get-report-descriptor", "--close"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), b, &out, "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(b.Opened, []string{"b"}) || d.Closed != 1 {
		t.Fatalf("device lifecycle %+v %+v", b, d)
	}
	if !reflect.DeepEqual(d.Writes, [][]byte{{17, 255, 0, 0}}) || !reflect.DeepEqual(d.Features, [][]byte{{3, 99, 0}}) || !reflect.DeepEqual(d.ReportIDs, []byte{3, 4}) {
		t.Fatalf("reports: %+v", d)
	}
	if !strings.Contains(out.String(), "Read 2 bytes") {
		t.Fatal(out.String())
	}
}

func TestListingFiltersAndEscaping(t *testing.T) {
	d := usbtest.Light(`a\"path`, "two")
	d.Product = "quote\"\nproduct"
	b := &usbtest.Backend{Infos: []usb.Info{usbtest.Light("a", "one"), d}}
	p, err := Parse([]string{"--vidpid=046d:c900", "--serial", "two", "--list-json"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), b, &out, "test"); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Devices []struct {
			Path    string
			Product string `json:"product_string"`
			VID     string `json:"vendor_id"`
		}
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 1 || result.Devices[0].Path != d.Path || result.Devices[0].Product != d.Product || result.Devices[0].VID != "0x046D" {
		t.Fatalf("bad listing: %s", out.String())
	}
	for _, option := range []string{"--list", "--list-usages", "--list-detail", "--version", "--help"} {
		p, err := Parse([]string{option})
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Run(context.Background(), b, io.Discard, "test"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidationBeforeAnyUSBWork(t *testing.T) {
	for _, args := range [][]string{
		nil, {"--open", "--send-output", "1,256"}, {"--open", "--length", "1", "--send-output", "1,2"},
		{"--open", "--send-output", "1", "--bogus"}, {"--open", "--read-feature", "256"},
		{"--length", "-1"}, {"--length", "4097"}, {"--timeout", "-2"}, {"--width", "0"}, {"--base", "8"},
		{"--read-input"}, {"--open", "--close", "--read-input"}, {"--open", "--open"},
		{"--open", "--length", "0", "--read-input"}, {"--open-path"}, {"--quiet=true"}, {"--vidpid", "10000:1", "--list"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("accepted invalid %v", args)
		}
	}
}

func TestInferredLengthQuietFormatAndReopen(t *testing.T) {
	d := &usbtest.Device{Reads: [][]byte{{1, 255}}}
	b := &usbtest.Backend{Devices: map[string]*usbtest.Device{"a": d}}
	p, err := Parse([]string{"-qv", "--open-path=a", "--length=0", "--send-output", "1,2", "--base", "10", "-w1", "--read-input", "--close", "--open-path", "a", "--close"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), b, &out, "test"); err != nil {
		t.Fatal(err)
	}
	if len(d.Writes[0]) != 2 || out.String() != "   1\n 255\n" || d.Closed != 2 {
		t.Fatalf("out=%q, device=%+v", out.String(), d)
	}
}

func TestReadErrorsTimeoutAndCancellation(t *testing.T) {
	for _, read := range []string{"--read-input-forever", "--read-input-report-forever"} {
		ctx, cancel := context.WithCancel(context.Background())
		d := &usbtest.Device{OnRead: cancel}
		b := &usbtest.Backend{Devices: map[string]*usbtest.Device{"a": d}}
		args := []string{"--open-path", "a", "--timeout", "-1", read}
		if read == "--read-input-report-forever" {
			args = append(args, "17")
		}
		p, err := Parse(args)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Run(ctx, b, io.Discard, "test"); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
		if d.Closed != 1 {
			t.Fatal("device leaked on cancellation")
		}
	}
	d := &usbtest.Device{}
	b := &usbtest.Backend{Devices: map[string]*usbtest.Device{"a": d}}
	p, _ := Parse([]string{"--quiet", "--open-path", "a", "--timeout", "0", "--read-input"})
	var out bytes.Buffer
	if err := p.Run(context.Background(), b, &out, "test"); err != nil || out.Len() != 0 {
		t.Fatalf("timeout output=%q err=%v", out.String(), err)
	}
	d.ReadError = errors.New("unplugged")
	if err := p.Run(context.Background(), b, io.Discard, "test"); err == nil {
		t.Fatal("read error swallowed")
	}
	d.ShortWrite = true
	p, _ = Parse([]string{"--open-path", "a", "--send-output", "1"})
	if err := p.Run(context.Background(), b, io.Discard, "test"); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
}
