package loader

import (
	"fmt"

	"github.com/expr-lang/expr"

	"gocordis"
)

// eval runs one !!js expression against the loader environment plus ctx.
func (l *Loader) eval(ctx *cordis.Context, source string) (any, error) {
	env := make(map[string]any, len(l.env)+1)
	for name, value := range l.env {
		env[name] = value
	}
	env["ctx"] = ctx

	program, err := expr.Compile(source, expr.Env(env), expr.AllowUndefinedVariables())
	if err != nil {
		return nil, fmt.Errorf("evaluate %q: %w", source, err)
	}
	value, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("evaluate %q: %w", source, err)
	}
	return value, nil
}

// interpolate replaces !!js scalars inside configs, recursively.
func (l *Loader) interpolate(ctx *cordis.Context, value any) (any, error) {
	switch typed := value.(type) {
	case Expr:
		return l.eval(ctx, string(typed))
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			interpolated, err := l.interpolate(ctx, item)
			if err != nil {
				return nil, err
			}
			result[i] = interpolated
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			interpolated, err := l.interpolate(ctx, item)
			if err != nil {
				return nil, err
			}
			result[key] = interpolated
		}
		return result, nil
	default:
		return value, nil
	}
}
