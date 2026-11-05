package cache

import (
	"context"
	"time"
)

// noop cache used when redis is not configured; every read is a miss
type Noop struct{}

// always a miss
func (Noop) Get(context.Context, string, any) error { return ErrMiss }

// discard writes
func (Noop) Set(context.Context, string, any, time.Duration) error { return nil }

// nothing to delete
func (Noop) Delete(context.Context, ...string) error { return nil }

// nothing to delete
func (Noop) DeleteByPattern(context.Context, string) (int, error) { return 0, nil }
