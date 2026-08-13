package action

import "context"

type Action interface {
	Activate(context.Context) error
	Deactivate(context.Context) error
}
