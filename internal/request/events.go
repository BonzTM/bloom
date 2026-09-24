package request

import (
	"context"
	"sync"

	"github.com/BonzTM/bloom/internal/core"
)

// EventHandler consumes one in-process request lifecycle event.
type EventHandler func(context.Context, core.RequestEvent)

// EventBus publishes request lifecycle events synchronously in registration order.
type EventBus struct {
	mu       sync.RWMutex
	handlers []EventHandler
}

// Subscribe adds one in-process consumer.
func (b *EventBus) Subscribe(handler EventHandler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, handler)
}

// PublishRequestEvent sends a value copy to every current consumer.
func (b *EventBus) PublishRequestEvent(ctx context.Context, event core.RequestEvent) {
	b.mu.RLock()
	handlers := append([]EventHandler(nil), b.handlers...)
	b.mu.RUnlock()
	for _, handler := range handlers {
		handler(ctx, event)
	}
}
