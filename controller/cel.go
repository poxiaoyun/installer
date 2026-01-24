package controller

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

type CELData struct {
	Instance  map[string]any   `json:"instance,omitempty"`
	Resources []map[string]any `json:"resources,omitempty"`
	Values    map[string]any   `json:"values,omitempty"`
}

func EvalCELExpression(expr string, data CELData) (any, error) {
	envs := []cel.EnvOption{
		cel.Variable("instance", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("resources", cel.ListType(cel.DynType)),
		cel.Variable("values", cel.MapType(cel.StringType, cel.DynType)),
	}
	env, err := cel.NewEnv(envs...)
	if err != nil {
		return nil, err
	}
	ast, iss := env.Compile(expr)
	if err := iss.Err(); err != nil {
		return nil, err
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, err
	}
	// Extract values from instance if not provided separately
	valuesData := data.Values
	if valuesData == nil {
		if vals, ok := data.Instance["values"].(map[string]any); ok {
			valuesData = vals
		} else {
			valuesData = map[string]any{}
		}
	}
	out, _, err := prg.Eval(map[string]any{
		"instance":  data.Instance,
		"resources": data.Resources,
		"values":    valuesData,
	})
	if err != nil {
		return nil, err
	}
	return ConvertCELResultToGo(out), nil
}

func ConvertCELResultToGo(val ref.Val) any {
	value := val.Value()
	switch v := value.(type) {
	case []ref.Val:
		result := make([]any, 0, len(v))
		for _, item := range v {
			result = append(result, ConvertCELResultToGo(item))
		}
		return result
	case map[ref.Val]ref.Val:
		result := make(map[string]any, len(v))
		for key, val := range v {
			valstring, ok := key.(types.String)
			if !ok {
				continue
			}
			result[string(valstring)] = ConvertCELResultToGo(val)
		}
		return result
	}
	return value
}
