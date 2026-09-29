// SPDX-License-Identifier: BSD-3-Clause
package usb

import (
	"encoding/hex"
	"reflect"
	"testing"
)

// Captured from a Litra Glow through IOHIDDeviceGetProperty.
const litraDescriptorHex = "050c0901a101a1030930850115ff2501950175029126091f8100950175069103091f8103c0a1030930850215002501950175019106950175079103c0c00643ff0a0202a101851175089513150026ff000902810009029100c0"

func TestLitraDescriptor(t *testing.T) {
	descriptor, _ := hex.DecodeString(litraDescriptorHex)
	got, err := descriptorUsages(descriptor)
	if err != nil || !reflect.DeepEqual(got, []usagePair{{0xc, 1}, {0xff43, 0x202}}) {
		t.Fatalf("usages = %v, %v", got, err)
	}
}
func TestDescriptorExtendedUsageAndStack(t *testing.T) {
	got, err := descriptorUsages([]byte{0x05, 1, 0xa4, 0x06, 0x43, 0xff, 0x0b, 2, 2, 0x43, 0xff, 0xa1, 1, 0xc0, 0xb4, 0x09, 2, 0xa1, 1, 0xc0})
	if err != nil || !reflect.DeepEqual(got, []usagePair{{0xff43, 0x202}, {1, 2}}) {
		t.Fatalf("usages = %v, %v", got, err)
	}
	for _, data := range [][]byte{{0xfe}, {0xfe, 5, 0}, {0x06, 1}, {0xb4}, {0xc0}, {0xa1, 1}} {
		if _, err := descriptorUsages(data); err == nil {
			t.Errorf("accepted malformed %x", data)
		}
	}
}
