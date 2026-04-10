package poller

import "context"

// Poller defines the interface for platform pollers
type Poller interface {
	// Poll fetches new entries from the platform
	Poll(ctx context.Context) error

	// Name returns the poller name for logging
	Name() string
}
