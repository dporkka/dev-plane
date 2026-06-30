// ingress_consumer_daemon.go -- Local ingress consumer for mesh node telemetry.
//
// This daemon subscribes to the NATS JetStream subject wildcard
// `agents.telemetry.>`, decodes JSON telemetry frames, and writes each frame to
// the NATS KV bucket `nodes.inventory` using Compare-And-Swap (CAS) semantics.
// The CAS is implemented via the bucket's sequence revision tracking so that
// concurrent writers cannot silently overwrite one another's updates.
//
// Build: go build -o ingress-consumer ./ingress_consumer_daemon.go
// Run:   ./ingress-consumer -nats nats://100.64.0.1:4222 -bucket nodes.inventory
//
// Dependencies:
//   go get github.com/nats-io/nats.go@latest

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	defaultNatsURL      = "nats://127.0.0.1:4222"
	defaultBucket       = "nodes.inventory"
	defaultStreamName   = "telemetry"
	defaultSubject      = "agents.telemetry.>"
	defaultConsumer     = "ingress-consumer"
	defaultMaxDeliver   = 3
	defaultAckWait      = 30 * time.Second
	defaultFetchSize    = 256
	defaultReconnectBuf = 32 * 1024 * 1024 // 32 MiB
	defaultCASAttempts  = 7
)

// NodeTelemetry describes the expected JSON payload published to
// agents.telemetry.{node_type}.{owner}.{session_id}.
type NodeTelemetry struct {
	NodeID      string            `json:"node_id"`
	NodeType    string            `json:"node_type"`
	Owner       string            `json:"owner"`
	SessionID   string            `json:"session_id"`
	Timestamp   time.Time         `json:"timestamp"`
	Hostname    string            `json:"hostname,omitempty"`
	TailscaleIP string            `json:"tailscale_ip,omitempty"`
	Cores       int               `json:"cores,omitempty"`
	MemoryBytes int64             `json:"memory_bytes,omitempty"`
	LoadAvg     []float64         `json:"load_avg,omitempty"`
	AgentCount  int               `json:"agent_count,omitempty"`
	Status      string            `json:"status,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// KV entry value wrapper; wraps the raw telemetry so the bucket stores
// well-formed JSON regardless of future schema changes.
type InventoryRecord struct {
	UpdatedAt time.Time     `json:"updated_at"`
	Revision  uint64        `json:"revision"`
	Telemetry NodeTelemetry `json:"telemetry"`
}

// Config holds runtime configuration for the daemon.
type Config struct {
	NatsURL      string
	BucketName   string
	StreamName   string
	Subject      string
	ConsumerName string
	MaxDeliver   int
	AckWait      time.Duration
	FetchSize    int
}

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.NatsURL, "nats", defaultNatsURL, "NATS server URL")
	flag.StringVar(&cfg.BucketName, "bucket", defaultBucket, "NATS KV bucket name")
	flag.StringVar(&cfg.StreamName, "stream", defaultStreamName, "JetStream telemetry stream name")
	flag.StringVar(&cfg.Subject, "subject", defaultSubject, "Telemetry subject wildcard")
	flag.StringVar(&cfg.ConsumerName, "consumer", defaultConsumer, "Durable consumer name")
	flag.IntVar(&cfg.MaxDeliver, "max-deliver", defaultMaxDeliver, "Max delivery attempts per message")
	flag.DurationVar(&cfg.AckWait, "ack-wait", defaultAckWait, "Acknowledgement wait timeout")
	flag.IntVar(&cfg.FetchSize, "fetch-size", defaultFetchSize, "Max messages per fetch")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Propagate OS signals so in-flight messages can be acked/nacked cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, initiating graceful shutdown", slog.String("signal", sig.String()))
		cancel()
	}()

	if err := run(ctx, logger, cfg); err != nil {
		logger.Error("daemon terminated with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// run is the main lifecycle: connect, ensure stream/bucket/consumer, consume loop.
func run(ctx context.Context, logger *slog.Logger, cfg Config) error {
	nc, err := connectWithBackoff(ctx, logger, cfg.NatsURL)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	defer nc.Drain()

	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("create JetStream context: %w", err)
	}

	stream, err := ensureTelemetryStream(ctx, js, cfg)
	if err != nil {
		return fmt.Errorf("ensure telemetry stream: %w", err)
	}

	kv, err := ensureInventoryBucket(ctx, js, cfg.BucketName)
	if err != nil {
		return fmt.Errorf("ensure inventory bucket: %w", err)
	}

	cons, err := ensureConsumer(ctx, stream, cfg)
	if err != nil {
		return fmt.Errorf("ensure consumer: %w", err)
	}

	logger.Info("ingress consumer ready",
		slog.String("subject", cfg.Subject),
		slog.String("bucket", cfg.BucketName),
	)

	return consumeLoop(ctx, logger, cfg, cons, kv)
}

// connectWithBackoff establishes a NATS connection with automatic reconnects
// and bounded initial-retry backoff.
func connectWithBackoff(ctx context.Context, logger *slog.Logger, natsURL string) (*nats.Conn, error) {
	if _, err := url.Parse(natsURL); err != nil {
		return nil, fmt.Errorf("invalid nats url: %w", err)
	}

	opts := []nats.Option{
		nats.Name("ingress-consumer-daemon"),
		nats.ReconnectBufSize(defaultReconnectBuf),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(1 * time.Second),
		nats.ReconnectJitter(500*time.Millisecond, 2*time.Second),
		nats.Timeout(5 * time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			logger.Warn("nats disconnected", slog.String("error", err.Error()))
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info("nats reconnected", slog.String("url", nc.ConnectedUrlRedacted()))
		}),
		nats.ErrorHandler(func(nc *nats.Conn, s *nats.Subscription, err error) {
			logger.Error("nats async error", slog.String("error", err.Error()), slog.String("sub", s.Subject))
		}),
	}

	backoff := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second, 2 * time.Second, 5 * time.Second}
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		nc, err := nats.Connect(natsURL, opts...)
		if err == nil {
			logger.Info("connected to NATS", slog.String("url", nc.ConnectedUrlRedacted()))
			return nc, nil
		}

		wait := backoff[len(backoff)-1]
		if attempt < len(backoff) {
			wait = backoff[attempt]
		}
		attempt++
		logger.Warn("nats connect failed, retrying",
			slog.String("error", err.Error()),
			slog.Duration("wait", wait),
			slog.Int("attempt", attempt),
		)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// ensureTelemetryStream creates or updates the telemetry JetStream stream.
func ensureTelemetryStream(ctx context.Context, js jetstream.JetStream, cfg Config) (jetstream.Stream, error) {
	cfgStream := jetstream.StreamConfig{
		Name:       cfg.StreamName,
		Subjects:   []string{cfg.Subject},
		Retention:  jetstream.WorkQueuePolicy,
		MaxMsgs:    -1,
		MaxBytes:   -1,
		MaxAge:     24 * time.Hour,
		Storage:    jetstream.MemoryStorage,
		Replicas:   1,
		Discard:    jetstream.DiscardOld,
		Duplicates: 2 * time.Minute,
	}
	stream, err := js.CreateOrUpdateStream(ctx, cfgStream)
	if err != nil {
		return nil, err
	}
	return stream, nil
}

// ensureInventoryBucket creates the KV bucket if it does not already exist.
func ensureInventoryBucket(ctx context.Context, js jetstream.JetStream, bucket string) (jetstream.KeyValue, error) {
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:       bucket,
		Description:  "Agent mesh node inventory with CAS revision tracking",
		MaxValueSize: 128 * 1024,
		History:      5,
		TTL:          0,
		MaxBytes:     -1,
		Storage:      jetstream.FileStorage,
		Replicas:     1,
	})
	if err != nil {
		// Bucket may already exist; try to bind.
		if errors.Is(err, jetstream.ErrBucketExists) {
			return js.KeyValue(ctx, bucket)
		}
		return nil, err
	}
	return kv, nil
}

// ensureConsumer creates a durable pull consumer over the telemetry stream.
func ensureConsumer(ctx context.Context, stream jetstream.Stream, cfg Config) (jetstream.Consumer, error) {
	return stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       cfg.ConsumerName,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxDeliver:    cfg.MaxDeliver,
		AckWait:       cfg.AckWait,
		FilterSubject: cfg.Subject,
		MaxAckPending: cfg.FetchSize,
		ReplayPolicy:  jetstream.ReplayInstantPolicy,
	})
}

// consumeLoop fetches telemetry messages and commits them to KV with CAS.
func consumeLoop(ctx context.Context, logger *slog.Logger, cfg Config, cons jetstream.Consumer, kv jetstream.KeyValue) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 1)

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case err := <-errCh:
			wg.Wait()
			return err
		default:
		}

		// Fetch a batch with context-aware deadline.
		msgs, err := cons.Fetch(cfg.FetchSize, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return fmt.Errorf("consumer fetch: %w", err)
		}

		for msg := range msgs.Messages() {
			m := msg // capture for goroutine
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := processMessage(ctx, logger, m, kv); err != nil {
					logger.Error("failed to process telemetry message",
						slog.String("subject", m.Subject()),
						slog.String("error", err.Error()),
					)
					nackErr := m.NakWithDelay(5 * time.Second)
					if nackErr != nil {
						logger.Error("failed to nack message", slog.String("error", nackErr.Error()))
					}
					return
				}
				if err := m.Ack(); err != nil {
					logger.Error("failed to ack message", slog.String("error", err.Error()))
				}
			}()
		}

		if msgs.Error() != nil {
			logger.Warn("fetch batch error", slog.String("error", msgs.Error().Error()))
		}
	}
}

// processMessage decodes a telemetry frame and commits it to the inventory bucket
// using a CAS loop over the KV revision.
func processMessage(ctx context.Context, logger *slog.Logger, msg jetstream.Msg, kv jetstream.KeyValue) error {
	var tel NodeTelemetry
	if err := json.Unmarshal(msg.Data(), &tel); err != nil {
		return fmt.Errorf("decode telemetry json: %w", err)
	}

	if tel.NodeID == "" {
		return errors.New("telemetry missing node_id")
	}
	if tel.Timestamp.IsZero() {
		tel.Timestamp = time.Now().UTC()
	}

	key := inventoryKey(tel)

	for attempt := 1; attempt <= defaultCASAttempts; attempt++ {
		entry, err := kv.Get(ctx, key)
		var last uint64
		var existing InventoryRecord

		switch {
		case err == nil:
			last = entry.Revision()
			if derr := json.Unmarshal(entry.Value(), &existing); derr != nil {
				logger.Warn("existing inventory record corrupt, overwriting",
					slog.String("key", key),
					slog.String("error", derr.Error()),
				)
				last = 0
			}
		case errors.Is(err, jetstream.ErrKeyNotFound):
			last = 0
		default:
			return fmt.Errorf("kv get %s: %w", key, err)
		}

		record := InventoryRecord{
			UpdatedAt: time.Now().UTC(),
			Revision:  last + 1,
			Telemetry: tel,
		}
		if existing.UpdatedAt.After(tel.Timestamp) {
			// Do not overwrite fresher inventory state with stale telemetry.
			record.Telemetry = existing.Telemetry
		}

		payload, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("marshal inventory record: %w", err)
		}

		if last == 0 {
			_, err = kv.Create(ctx, key, payload)
		} else {
			_, err = kv.Update(ctx, key, payload, last)
		}
		if err == nil {
			logger.Info("inventory updated",
				slog.String("key", key),
				slog.Uint64("revision", record.Revision),
			)
			return nil
		}

		if !isCASConflict(err) {
			return fmt.Errorf("kv commit %s: %w", key, err)
		}
		if attempt == defaultCASAttempts {
			break
		}

		logger.Warn("kv cas conflict, retrying",
			slog.String("key", key),
			slog.Int("attempt", attempt),
			slog.String("error", err.Error()),
		)
		if err := sleepCtx(ctx, casBackoff(attempt)); err != nil {
			return err
		}
	}

	return fmt.Errorf("cas retries exhausted for key %s", key)
}

// isCASConflict reports whether err is a retriable revision/key collision.
func isCASConflict(err error) bool {
	return errors.Is(err, jetstream.ErrKeyExists) || errors.Is(err, jetstream.ErrBadRequest)
}

// casBackoff returns an exponential retry delay with small jitter.
func casBackoff(attempt int) time.Duration {
	base := []time.Duration{
		10 * time.Millisecond,
		25 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
	}
	i := attempt - 1
	if i >= len(base) {
		i = len(base) - 1
	}
	jitter := time.Duration(rand.Intn(20)) * time.Millisecond
	return base[i] + jitter
}

// sleepCtx sleeps for d or until ctx is canceled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// inventoryKey returns the KV key for a telemetry record. The key is stable
// per node so repeated telemetry frames converge to the same inventory row.
func inventoryKey(tel NodeTelemetry) string {
	return fmt.Sprintf("%s.%s.%s", tel.NodeType, tel.Owner, tel.NodeID)
}
