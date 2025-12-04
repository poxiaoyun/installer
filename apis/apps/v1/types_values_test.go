package v1

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDeepCopy_NilReceiver(t *testing.T) {
	var v *Values = nil
	if got := v.DeepCopy(); got != nil {
		t.Fatalf("DeepCopy(nil) = %v, want nil", got)
	}
}

func TestDeepCopy_ObjectNil(t *testing.T) {
	v := &Values{}
	got := v.DeepCopy()
	if got == nil {
		t.Fatalf("DeepCopy returned nil pointer for non-nil receiver")
	}
	if got.Object != nil {
		t.Fatalf("DeepCopy on Values with nil Object returned Object=%v, want nil", got.Object)
	}
}

func TestDeepCopy_Deepness(t *testing.T) {
	orig := &Values{
		Object: map[string]any{
			"s": "v",
			"m": map[string]any{
				"x": 1.0,
			},
		},
	}
	cp := orig.DeepCopy()
	if cp == nil {
		t.Fatalf("DeepCopy returned nil")
	}
	if !reflect.DeepEqual(orig.Object, cp.Object) {
		t.Fatalf("copy not equal to original initially: got %v want %v", cp.Object, orig.Object)
	}

	orig.Object["s"] = "changed"
	if m, ok := orig.Object["m"].(map[string]any); ok {
		m["x"] = 2.0
	} else {
		t.Fatalf("unexpected type for orig.Object[\"m\"]")
	}

	if cpVal, _ := cp.Object["s"].(string); cpVal != "v" {
		t.Fatalf("deep copy top-level value changed: got %v want %v", cpVal, "v")
	}
	nested, _ := cp.Object["m"].(map[string]any)
	if nested["x"] != 1.0 {
		t.Fatalf("deep copy nested map changed: got %v want %v", nested["x"], 1.0)
	}
}

func TestUnmarshalJSON_NilReceiver(t *testing.T) {
	var v *Values = nil
	err := v.UnmarshalJSON([]byte(`{}`))
	if err == nil {
		t.Fatalf("expected error when calling UnmarshalJSON on nil receiver, got nil")
	}
	if err.Error() != "Values: UnmarshalJSON on nil pointer" {
		t.Fatalf("unexpected error message: got %q", err.Error())
	}
}

func TestUnmarshalJSON_NullInputDoesNotOverwrite(t *testing.T) {
	v := &Values{
		Object: map[string]any{"a": "b"},
	}
	err := v.UnmarshalJSON([]byte("null"))
	if err != nil {
		t.Fatalf("UnmarshalJSON(null) returned error: %v", err)
	}
	if v.Object != nil {
		t.Fatalf("UnmarshalJSON(null) did not set Object to nil: got %v", v.Object)
	}
}

func TestUnmarshalJSON_ValidAndInvalid(t *testing.T) {
	v := &Values{}
	err := v.UnmarshalJSON([]byte(`{"a":"b","num":42}`))
	if err != nil {
		t.Fatalf("UnmarshalJSON valid json returned error: %v", err)
	}
	if v.Object == nil {
		t.Fatalf("expected Object to be non-nil after UnmarshalJSON")
	}
	if v.Object["a"] != "b" {
		t.Fatalf("unexpected value for 'a': got %v want %v", v.Object["a"], "b")
	}
	if n, ok := v.Object["num"].(float64); !ok || n != 42.0 {
		t.Fatalf("unexpected value/type for 'num': got %v (ok=%v) want 42.0", v.Object["num"], ok)
	}

	err = v.UnmarshalJSON([]byte(`{invalid`))
	if err == nil {
		t.Fatalf("expected error for invalid JSON, got nil")
	}
}

func TestMarshalJSON_ObjectNilAndNonNil(t *testing.T) {
	v1 := Values{}
	b, err := v1.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON returned error for nil Object: %v", err)
	}
	if string(b) != "{}" {
		t.Fatalf("MarshalJSON for nil Object = %s, want %s", string(b), "{}")
	}

	v2 := Values{
		Object: map[string]any{
			"a": "b",
			"m": map[string]any{"x": 1.0},
		},
	}
	b2, err := v2.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON returned error for non-nil Object: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b2, &out); err != nil {
		t.Fatalf("unmarshal of MarshalJSON output failed: %v", err)
	}
	if !reflect.DeepEqual(out, v2.Object) {
		t.Fatalf("marshal/unmarshal roundtrip mismatch: got %v want %v", out, v2.Object)
	}
}
