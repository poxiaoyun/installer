package utils

import "k8s.io/apimachinery/pkg/api/equality"

func EqualMapValues(a, b map[string]any) bool {
	return equality.Semantic.DeepEqual(a, b)
}
