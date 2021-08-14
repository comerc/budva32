package utils

import utf16 "unicode/utf16"

func StrLen(s string) int {
	return len(utf16.Encode([]rune(s)))
}
