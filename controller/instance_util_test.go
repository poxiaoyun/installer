package controller

import (
	"reflect"
	"testing"
)

func TestCleanNilValues(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name: "flat map with nil",
			input: map[string]any{
				"a": 1,
				"b": nil,
				"c": "foo",
			},
			expected: map[string]any{
				"a": 1,
				"c": "foo",
			},
		},
		{
			name: "nested map with nil",
			input: map[string]any{
				"a": 1,
				"b": map[string]any{
					"b1": nil,
					"b2": "bar",
				},
				"c": nil,
			},
			expected: map[string]any{
				"a": 1,
				"b": map[string]any{
					"b2": "bar",
				},
			},
		},
		{
			name: "deeply nested map with nil",
			input: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"c": nil,
						"d": 100,
					},
				},
			},
			expected: map[string]any{
				"a": map[string]any{
					"b": map[string]any{
						"d": 100,
					},
				},
			},
		},
		{
			name: "no nil values",
			input: map[string]any{
				"a": 1,
				"b": "foo",
				"c": map[string]any{"d": 2},
			},
			expected: map[string]any{
				"a": 1,
				"b": "foo",
				"c": map[string]any{"d": 2},
			},
		},
		{
			name:     "empty map",
			input:    map[string]any{},
			expected: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanNilValues(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("cleanNilValues() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMergeMaps(t *testing.T) {
	tests := []struct {
		name     string
		a        map[string]any
		b        map[string]any
		expected map[string]any
	}{
		{
			name:     "flat merge",
			a:        map[string]any{"a": 1, "b": 2},
			b:        map[string]any{"b": 3, "c": 4},
			expected: map[string]any{"a": 1, "b": 3, "c": 4},
		},
		{
			name:     "nested merge",
			a:        map[string]any{"a": map[string]any{"a1": 1}},
			b:        map[string]any{"a": map[string]any{"a2": 2}},
			expected: map[string]any{"a": map[string]any{"a1": 1, "a2": 2}},
		},
		{
			name:     "recursive nested merge",
			a:        map[string]any{"a": map[string]any{"b": map[string]any{"c1": 1}}},
			b:        map[string]any{"a": map[string]any{"b": map[string]any{"c2": 2}}},
			expected: map[string]any{"a": map[string]any{"b": map[string]any{"c1": 1, "c2": 2}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeMaps(tt.a, tt.b)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("mergeMaps() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMergeInto(t *testing.T) {
	base := map[string]any{
		"foo": "bar",
	}
	err := mergeInto("a.b.c", "val", base)
	if err != nil {
		t.Fatalf("mergeInto failed: %v", err)
	}

	expected := map[string]any{
		"foo": "bar",
		"a": map[string]any{
			"b": map[string]any{
				"c": "val",
			},
		},
	}

	if !reflect.DeepEqual(base, expected) {
		t.Errorf("mergeInto() = %v, want %v", base, expected)
	}
}
