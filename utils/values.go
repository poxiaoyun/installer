package utils

import "reflect"

func EqualMapValues(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	return reflect.DeepEqual(a, b)
}
