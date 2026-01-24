package controller

import (
	"reflect"
	"testing"
)

func TestEvalCELExpression(t *testing.T) {
	data := CELData{
		Instance: map[string]any{
			"name": "test-instance",
			"values": map[string]any{
				"foo": "bar",
			},
		},
		Resources: []map[string]any{
			{
				"kind": "Service",
				"metadata": map[string]any{
					"name": "test-svc",
				},
			},
		},
		Values: map[string]any{
			"foo": "bar",
		},
	}

	tests := []struct {
		name    string
		expr    string
		want    any
		wantErr bool
	}{
		{
			name: "simple value access",
			expr: "values.foo",
			want: "bar",
		},
		{
			name: "resource access",
			expr: "resources[0].kind",
			want: "Service",
		},
		{
			name: "list construction",
			expr: "[{'name': 'test', 'status': 'Running'}]",
			want: []any{
				map[string]any{
					"name":   "test",
					"status": "Running",
				},
			},
		},
		{
			name:    "invalid syntax",
			expr:    "values.foo +",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvalCELExpression(tt.expr, data)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvalCELExpression() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("EvalCELExpression() = %v, want %v", got, tt.want)
			}
		})
	}
}
