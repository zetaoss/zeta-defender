package metrics

import "context"

type Provider interface {
	Evaluate(context.Context) (bool, error)
}
