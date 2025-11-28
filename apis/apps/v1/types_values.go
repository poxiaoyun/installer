package v1

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
)

// Values represents a set of arbitrary values.
type Values struct {
	Object map[string]any `json:"-"`
}

func init() {
	// https://pkg.go.dev/encoding/gob#Register
	gob.Register(map[string]any{})
}

// DeepCopy indicate how to do a deep copy of Values type
func (v *Values) DeepCopy() *Values {
	if v == nil {
		return nil
	}
	out := Values{}
	if v.Object != nil {
		out.Object = make(map[string]any, len(v.Object))
		for k, val := range v.Object {
			out.Object[k] = deepCopyAny(val)
		}
	}
	return &out
}

// deepCopyAny performs a recursive deep copy for common JSON-like structures:
// - map[string]any -> new map with deep-copied values
// - []any -> new slice with deep-copied elements
// - map[interface{}]any -> converted to map[string]any via fmt.Sprint on keys
// Other values are returned as-is (assumed immutable/basic types).
func deepCopyAny(in any) any {
	switch v := in.(type) {
	case map[string]any:
		m := make(map[string]any, len(v))
		for kk, vv := range v {
			m[kk] = deepCopyAny(vv)
		}
		return m
	case []any:
		s := make([]any, len(v))
		for i, vv := range v {
			s[i] = deepCopyAny(vv)
		}
		return s
	case map[any]any:
		// If there are maps with interface{} keys, convert keys to strings.
		m := make(map[string]any, len(v))
		for kk, vv := range v {
			m[fmt.Sprint(kk)] = deepCopyAny(vv)
		}
		return m
	default:
		return v
	}
}

func (v *Values) UnmarshalJSON(in []byte) error {
	if v == nil {
		return errors.New("Values: UnmarshalJSON on nil pointer")
	}
	if bytes.Equal(in, []byte("null")) {
		v.Object = nil
		return nil
	}
	val := map[string]any(nil)
	if err := json.Unmarshal(in, &val); err != nil {
		return err
	}
	v.Object = val
	return nil
}

func (re Values) MarshalJSON() ([]byte, error) {
	return json.Marshal(re.Object)
}
