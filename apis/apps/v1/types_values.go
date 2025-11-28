package v1

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
)

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
		buf := new(bytes.Buffer)
		gob.NewEncoder(buf).Encode(v.Object)
		gob.NewDecoder(buf).Decode(&out.Object)
	}
	return &out
}

func (v *Values) UnmarshalJSON(in []byte) error {
	if v == nil {
		return errors.New("Values: UnmarshalJSON on nil pointer")
	}
	if bytes.Equal(in, []byte("null")) {
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
	if re.Object != nil {
		return json.Marshal(re.Object)
	}
	return []byte("{}"), nil
}
