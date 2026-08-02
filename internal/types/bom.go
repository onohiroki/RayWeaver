package types

import "unicode/utf16"

// DecodeBOM strips a UTF-8/UTF-16LE/UTF-16BE BOM and decodes UTF-16 content
// into UTF-8 bytes. Content without a BOM is returned unchanged.
func DecodeBOM(raw []byte) []byte {
	if len(raw) < 2 {
		return raw
	}
	if raw[0] == 0xFF && raw[1] == 0xFE {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		return []byte(string(utf16.Decode(u16)))
	}
	if raw[0] == 0xFE && raw[1] == 0xFF {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])<<8|uint16(raw[i+1]))
		}
		return []byte(string(utf16.Decode(u16)))
	}
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}
