package webhooks

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/events"
	"github.com/nats-io/nats.go"
)

// New, disposable broker only; never use NATS_URL or an existing stream.
func disposableWebhookBus(t *testing.T) *events.Bus {
	t.Helper()
	binary := os.Getenv("DEVPLANE_TEST_NATS_SERVER")
	if binary == "" {
		t.Skip("set DEVPLANE_TEST_NATS_SERVER to an absolute path to a trusted nats-server executable")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("nats-server path must be absolute")
	}
	dir := t.TempDir()
	log, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.Command(binary, "-a", "127.0.0.1", "-p", "-1", "-js", "-sd", filepath.Join(dir, "jetstream"), "--ports_file_dir", dir)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		files, err := filepath.Glob(filepath.Join(dir, "*.ports"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		raw, err := os.ReadFile(files[0])
		if err != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		var ports struct {
			Nats []string `json:"nats"`
		}
		if err := json.Unmarshal(raw, &ports); err != nil || len(ports.Nats) != 1 {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		target, err := url.Parse(ports.Nats[0])
		if err != nil || target.Scheme != "nats" || target.Hostname() != "127.0.0.1" || target.Port() == "" || target.User != nil {
			t.Fatal("refusing non-loopback broker")
		}
		bus, err := events.New(target.String())
		if err != nil {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		t.Cleanup(func() { _ = bus.Close() })
		if err := bus.CreateStreams(); err != nil {
			t.Fatal(err)
		}
		return bus
	}
	output, _ := os.ReadFile(filepath.Join(dir, "server.log"))
	t.Fatalf("disposable NATS did not start: %s", output)
	return nil
}

func TestLiveGiteaReceiptJetStreamRedelivery(t *testing.T) {
	bus := disposableWebhookBus(t)
	database := setupReceiptDB(t)
	if _, err := database.Exec("ALTER TABLE gitea_webhook_receipts RENAME TO unavailable_receipts"); err != nil {
		t.Fatal(err)
	}
	consumer := NewConsumer(database, slog.Default(), bus).WithGiteaWebhook(receiptInstance, receiptSecret)

	// Emulate the pre-existing wildcard durable and prove the new manual ACK
	// subscription can bind without deleting or recreating that durable.
	const durable = "webhooks_star"
	_, err := bus.JetStream().AddConsumer(events.StreamWebhooks, &nats.ConsumerConfig{
		Durable: durable, DeliverSubject: nats.NewInbox(), FilterSubject: "webhooks.*",
		AckPolicy: nats.AckExplicitPolicy, AckWait: time.Second, MaxDeliver: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		subject   string
		delivered uint64
		err       error
	}
	attempts := make(chan result, 16)
	sub, err := bus.SubscribeManual("webhooks.*", func(msg *nats.Msg) {
		metadata, err := msg.Metadata()
		if err != nil {
			attempts <- result{err: err}
			return
		}
		attempts <- result{subject: msg.Subject, delivered: metadata.NumDelivered, err: consumer.Handle(msg)}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	await := func() result {
		t.Helper()
		select {
		case result := <-attempts:
			return result
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for broker delivery")
			return result{}
		}
	}
	send := func(subject string, event events.WebhookEvent) {
		t.Helper()
		if err := bus.Publish(subject, receiptMessage(event).Data); err != nil {
			t.Fatal(err)
		}
	}

	send(events.WebhookReceived, receiptEvent("delivery-1", receiptBody))
	first := await()
	if first.err == nil || first.delivered != 1 {
		t.Fatalf("expected a transient first failure: %+v", first)
	}
	if _, err := database.Exec("ALTER TABLE unavailable_receipts RENAME TO gitea_webhook_receipts"); err != nil {
		t.Fatal(err)
	}
	retry := await()
	if retry.err != nil || retry.delivered < 2 {
		t.Fatalf("expected actual broker redelivery: %+v", retry)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 1)

	replay := receiptEvent("changed-delivery", receiptBody)
	replay.EventType = "pull_request"
	send(events.WebhookReceived, replay)
	if got := await(); got.err != nil {
		t.Fatal(got.err)
	}
	other := receiptEvent("must-not-be-ingested", strings.Replace(receiptBody, "heads/main", "heads/other", 1))
	for _, subject := range []string{events.WebhookProcessed, events.WebhookFailed} {
		send(subject, other)
		if got := await(); got.err != nil || got.subject != subject {
			t.Fatalf("outcome not drained: %+v", got)
		}
	}

	// The consumer must remain idle through more than one ACK timeout.
	// A missing ACK or outcome feedback loop would produce another delivery.
	select {
	case got := <-attempts:
		t.Fatalf("unexpected repeat after successful ACK: %+v", got)
	case <-time.After(1500 * time.Millisecond):
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 1)
	assertSQLCount(t, database, "SELECT COUNT(*) FROM tasks", 0)
	info, err := bus.JetStream().ConsumerInfo(events.StreamWebhooks, durable)
	if err != nil || info.NumAckPending != 0 || info.NumPending != 0 {
		t.Fatalf("unsettled durable consumer: %+v, err=%v", info, err)
	}
}
