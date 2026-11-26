package events

import (
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"
)

// SubscribeManual uses the same durable identity as Subscribe but enables
// explicit acknowledgments. A callback returning after a transient failure
// without ACK must not cause the client to acknowledge that message.
func (b *Bus) SubscribeManual(subject string, handler nats.MsgHandler) (*nats.Subscription, error) {
	durableName := strings.NewReplacer(".", "_", "*", "star", ">", "all").Replace(subject)
	sub, err := b.js.Subscribe(subject, handler, nats.Durable(durableName), nats.ManualAck())
	if err != nil {
		return nil, fmt.Errorf("subscribe manually to %q: %w", subject, err)
	}
	return sub, nil
}
