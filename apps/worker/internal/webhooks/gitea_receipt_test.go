package webhooks

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/nats-io/nats.go"
)

const (
	receiptInstance = "https://git.example.test/code"
	receiptSecret   = "fixture-secret/"
	receiptBody     = "{  \"repository\": {\"id\":77,\"full_name\":\"acme/widget\",\"html_url\":\"https://git.example.test/code/acme/widget\"},\"ref\":\"refs/heads/main\" }\n"
)

func setupReceiptDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.New("file:" + filepath.Join(t.TempDir(), "receipt.db") + "?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.RunMigrations("../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}
	return database.DB
}

func receiptEvent(delivery, body string) events.WebhookEvent {
	return events.WebhookEvent{
		Source: "gitea", EventType: "push", DeliveryID: delivery, RepositoryID: "acme/widget",
		ForgeInstance: receiptInstance, SignedPayload: []byte(body),
		Signature: gateway.ComputeGiteaWebhookSignature([]byte(body), receiptSecret),
	}
}

func receiptMessage(event events.WebhookEvent) *nats.Msg {
	data, err := json.Marshal(event)
	if err != nil {
		panic(err)
	}
	return &nats.Msg{Subject: events.WebhookReceived, Data: data}
}

func assertSQLCount(t *testing.T, database *sql.DB, query string, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow(query).Scan(&count); err != nil || count != want {
		t.Fatalf("query %q count = %d, err=%v; want %d", query, count, err, want)
	}
}

func TestGiteaReceiptConcurrentReplay(t *testing.T) {
	database := setupReceiptDB(t)
	publisher := &fakeEventPublisher{}
	first := NewConsumer(database, slog.Default(), publisher).WithGiteaWebhook(receiptInstance+"/", receiptSecret)
	second := NewConsumer(database, slog.Default(), publisher).WithGiteaWebhook(receiptInstance, receiptSecret)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event := receiptEvent(fmt.Sprintf("delivery-%d", i), receiptBody)
			if i%2 == 0 {
				event.EventType = "pull_request_review_approved" // header is not signed
				event.Signature = gateway.ComputeGitHubWebhookSignature(event.SignedPayload, receiptSecret)
			}
			consumer := first
			if i%2 == 0 {
				consumer = second
			}
			if err := consumer.Handle(receiptMessage(event)); err != nil {
				t.Errorf("handle: %v", err)
			}
		}(i)
	}
	wg.Wait()
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 1)
	assertSQLCount(t, database, "SELECT COUNT(*) FROM tasks", 0)
	if len(publisher.subjects) != 0 {
		t.Fatal("receipt published an outcome or task event")
	}
	var instance, delivery, fullName, digest string
	var id int64
	if err := database.QueryRow("SELECT instance_url,delivery_id,repository_id,repository_full_name,payload_sha256 FROM gitea_webhook_receipts").
		Scan(&instance, &delivery, &id, &fullName, &digest); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte(receiptBody))
	if instance != receiptInstance || id != 77 || fullName != "acme/widget" || digest != hex.EncodeToString(expected[:]) {
		t.Fatal("incorrect durable receipt")
	}
	changed := strings.Replace(receiptBody, "heads/main", "heads/review", 1)
	if err := first.Handle(receiptMessage(receiptEvent(delivery, changed))); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 1)
	if err := first.Handle(receiptMessage(receiptEvent("new-delivery", changed))); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 2)
}

func TestGiteaReceiptRejectsUnboundAndMalformedEvents(t *testing.T) {
	database := setupReceiptDB(t)
	consumer := NewConsumer(database, slog.Default(), nil).WithGiteaWebhook(receiptInstance, receiptSecret)
	for _, tc := range []struct {
		name   string
		mutate func(*events.WebhookEvent)
		retry  bool
	}{
		{"foreign instance", func(e *events.WebhookEvent) { e.ForgeInstance = "https://other.example.test/code" }, true},
		{"wrong signed repository URL", func(e *events.WebhookEvent) {
			*e = receiptEvent("d", strings.Replace(receiptBody, receiptInstance, "https://other.example.test/code", 1))
		}, false},
		{"different envelope repository", func(e *events.WebhookEvent) { e.RepositoryID = "another/repository" }, false},
		{"tampered bytes", func(e *events.WebhookEvent) { e.SignedPayload = append(e.SignedPayload, ' ') }, false},
		{"wrong signature", func(e *events.WebhookEvent) {
			e.Signature = gateway.ComputeGiteaWebhookSignature(e.SignedPayload, "wrong")
		}, false},
		{"old ingress", func(e *events.WebhookEvent) { e.Payload = e.SignedPayload; e.SignedPayload = nil }, false},
		{"missing delivery", func(e *events.WebhookEvent) { e.DeliveryID = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := receiptEvent("d", receiptBody)
			tc.mutate(&event)
			if err := consumer.Handle(receiptMessage(event)); (err != nil) != tc.retry {
				t.Fatalf("handle err=%v, retry=%v", err, tc.retry)
			}
			assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 0)
		})
	}
	disabled := NewConsumer(database, slog.Default(), nil)
	if err := disabled.Handle(receiptMessage(receiptEvent("d", receiptBody))); err == nil {
		t.Fatal("unconfigured worker silently acknowledged receipt")
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 0)
	if err := consumer.Handle(receiptMessage(receiptEvent("d", receiptBody))); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 1)
}

func TestGiteaReceiptPersistenceFailureIsRetryable(t *testing.T) {
	database := setupReceiptDB(t)
	consumer := NewConsumer(database, slog.Default(), nil).WithGiteaWebhook(receiptInstance, receiptSecret)
	if _, err := database.Exec("ALTER TABLE gitea_webhook_receipts RENAME TO unavailable_receipts"); err != nil {
		t.Fatal(err)
	}
	msg := receiptMessage(receiptEvent("retry-delivery", receiptBody))
	if err := consumer.Handle(msg); err == nil {
		t.Fatal("missing receipt table reported success")
	}
	if _, err := database.Exec("ALTER TABLE unavailable_receipts RENAME TO gitea_webhook_receipts"); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Handle(msg); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM gitea_webhook_receipts", 1)
}

func TestWebhookOutcomeMessagesNeverReenterIngress(t *testing.T) {
	database := setupConsumerDB(t)
	t.Cleanup(func() { _ = database.Close() })
	insertWebhookFixtures(t, database)
	publisher := &fakeEventPublisher{}
	consumer := NewConsumer(database, slog.Default(), publisher)
	for _, subject := range []string{events.WebhookProcessed, events.WebhookFailed} {
		event := events.WebhookEvent{Source: "github", EventType: "issues", RepositoryID: "acme/app", Payload: json.RawMessage(`{"action":"opened","issue":{"number":1,"title":"must not dispatch","state":"open"}}`)}
		msg := receiptMessage(event)
		msg.Subject = subject
		if err := consumer.Handle(msg); err != nil {
			t.Fatal(err)
		}
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM tasks", 0)
	if len(publisher.subjects) != 0 {
		t.Fatal("outcome was republished")
	}
}

func TestGitHubWebhookNeverChoosesGiteaOrAmbiguousRepository(t *testing.T) {
	database := setupConsumerDB(t)
	t.Cleanup(func() { _ = database.Close() })
	insertWebhookFixtures(t, database)
	if _, err := database.Exec(`UPDATE repositories SET settings='{"forge":{"provider":"gitea","base_url":"https://git.example.test"}}' WHERE id='repo-1'`); err != nil {
		t.Fatal(err)
	}
	consumer := NewConsumer(database, slog.Default(), nil)
	event := events.WebhookEvent{Source: "github", EventType: "issues", RepositoryID: "acme/app", Payload: json.RawMessage(`{"action":"opened","issue":{"number":1,"title":"github issue","state":"open"}}`)}
	if err := consumer.Handle(receiptMessage(event)); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM tasks", 0)
	if _, err := database.Exec(`INSERT INTO repositories (id,project_id,full_name,clone_url) VALUES ('github-1','proj-1','acme/app','https://github.com/acme/app.git')`); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Handle(receiptMessage(event)); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM tasks WHERE repository_id='github-1'", 1)
	if _, err := database.Exec(`INSERT INTO repositories (id,project_id,full_name,clone_url) VALUES ('github-2','proj-1','acme/app','https://github.com/acme/app.git')`); err != nil {
		t.Fatal(err)
	}
	if err := consumer.Handle(receiptMessage(event)); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, database, "SELECT COUNT(*) FROM tasks", 1)
}
