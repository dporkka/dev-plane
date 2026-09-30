package agentruntime

import "context"

// Store persists provider-neutral agent conversations independently of the
// provider process lifecycle. Implementations must preserve the full payloads
// so providers can be resumed after control-plane restarts.
type Store interface {
	PutThread(ctx context.Context, thread Thread) error
	GetThread(ctx context.Context, id string) (Thread, error)

	PutTurn(ctx context.Context, turn Turn) error
	GetTurn(ctx context.Context, id string) (Turn, error)
	ListTurns(ctx context.Context, threadID string) ([]Turn, error)

	PutItem(ctx context.Context, item Item) error
	GetItem(ctx context.Context, id string) (Item, error)
	ListItems(ctx context.Context, turnID string) ([]Item, error)
}
