// SPDX-License-Identifier: BSD-3-Clause
package usb

import "fmt"

type usagePair struct{ page, usage uint16 }

// descriptorUsages reads top-level application collections from a HID report
// descriptor, including global push/pop and 32-bit extended local usages.
func descriptorUsages(data []byte) ([]usagePair, error) {
	var page uint16
	var stack []uint16
	var local []usagePair
	var pairs []usagePair
	depth := 0
	for i := 0; i < len(data); {
		prefix := data[i]
		i++
		if prefix == 0xfe {
			if i+2 > len(data) {
				return nil, fmt.Errorf("truncated HID long item")
			}
			size := int(data[i])
			i += 2
			if i+size > len(data) {
				return nil, fmt.Errorf("truncated HID long item payload")
			}
			i += size
			continue
		}
		size := int(prefix & 3)
		if size == 3 {
			size = 4
		}
		if i+size > len(data) {
			return nil, fmt.Errorf("truncated HID short item")
		}
		var value uint32
		for j := 0; j < size; j++ {
			value |= uint32(data[i+j]) << (8 * j)
		}
		i += size
		typ, tag := (prefix>>2)&3, prefix>>4
		switch typ {
		case 1:
			switch tag {
			case 0:
				page = uint16(value)
			case 10:
				stack = append(stack, page)
			case 11:
				if len(stack) == 0 {
					return nil, fmt.Errorf("HID global stack underflow")
				}
				page, stack = stack[len(stack)-1], stack[:len(stack)-1]
			}
		case 2:
			if tag == 0 || tag == 1 {
				p := usagePair{page, uint16(value)}
				if size == 4 {
					p.page = uint16(value >> 16)
				}
				local = append(local, p)
			}
		case 0:
			if tag == 10 {
				if depth == 0 && value == 1 && len(local) != 0 {
					pairs = append(pairs, local[0])
				}
				depth++
			} else if tag == 12 {
				depth--
				if depth < 0 {
					return nil, fmt.Errorf("HID collection underflow")
				}
			}
			local = nil
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unclosed HID collection")
	}
	return pairs, nil
}
