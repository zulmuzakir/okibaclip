//go:build !linux

package capture

import (
	"context"
	"errors"
)

// NewWaylandSource is only available on Linux.
func NewWaylandSource(context.Context) (Source, error) { return nil, errors.ErrUnsupported }
