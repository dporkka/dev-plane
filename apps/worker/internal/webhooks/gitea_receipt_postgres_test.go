package webhooks

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ai-dev-control-plane/db"
)

// Opt-in only against a fresh, disposable database on a private loopback
// PostgreSQL instance. No caller-supplied database or host overrides.
func TestPostgresGiteaReceiptConcurrentWriters(t *testing.T) {
	dsn := os.Getenv("DEVPLANE_TEST_GITEA_RECEIPTS_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set DEVPLANE_TEST_GITEA_RECEIPTS_POSTGRES_URL to a fresh disposable database")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "postgres" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" ||
		parsed.Path != "/devplane_gitea_receipts_test" || parsed.User == nil || parsed.User.Username() != "fixture" ||
		parsed.RawQuery != "sslmode=disable" || parsed.Fragment != "" {
		t.Fatal("refusing PostgreSQL test outside a dedicated loopback fixture database")
	}
	first, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	var databaseName, user string
	var tables int
	if err := first.QueryRow("SELECT current_database(),current_user,(SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public')").
		Scan(&databaseName, &user, &tables); err != nil || databaseName != "devplane_gitea_receipts_test" || user != "fixture" || tables != 0 {
		t.Fatal("refusing migrations outside an empty dedicated fixture database")
	}
	if err := first.RunMigrations("../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}
	second, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.QueryRow("SELECT current_database(),current_user").Scan(&databaseName, &user); err != nil || databaseName != "devplane_gitea_receipts_test" || user != "fixture" {
		t.Fatal("refusing second connection outside fixture database")
	}
	workers := []*Consumer{
		NewConsumer(first.DB, slog.Default(), nil).WithGiteaWebhook(receiptInstance, receiptSecret),
		NewConsumer(second.DB, slog.Default(), nil).WithGiteaWebhook(receiptInstance, receiptSecret),
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := workers[i%2].Handle(receiptMessage(receiptEvent(fmt.Sprintf("postgres-%d", i), receiptBody))); err != nil {
				t.Errorf("concurrent receipt: %v", err)
			}
		}(i)
	}
	wg.Wait()
	assertSQLCount(t, first.DB, "SELECT COUNT(*) FROM gitea_webhook_receipts", 1)
	assertSQLCount(t, second.DB, "SELECT COUNT(*) FROM tasks", 0)
	otherInstance := "https://other.example.test/code"
	var delivery string
	if err := first.QueryRow("SELECT delivery_id FROM gitea_webhook_receipts").Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	event := receiptEvent(delivery, strings.Replace(receiptBody, receiptInstance, otherInstance, 1))
	event.ForgeInstance = otherInstance
	other := NewConsumer(second.DB, slog.Default(), nil).WithGiteaWebhook(otherInstance, receiptSecret)
	if err := other.Handle(receiptMessage(event)); err != nil {
		t.Fatal(err)
	}
	assertSQLCount(t, first.DB, "SELECT COUNT(*) FROM gitea_webhook_receipts", 2)
}
