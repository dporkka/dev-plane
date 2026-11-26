package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/nats-io/nats.go"
)

func TestWorkerRoutingConfig(t *testing.T) {
	for _, key := range []string{"DEV_PLANE_WORKER_LANE", "DEV_PLANE_WORKER_WEBHOOKS", "FORGE_PROVIDER", "GITEA_URL", "GITEA_TOKEN",
		"GITEA_BINDING_GENERATION", "GITEA_CLONE_TOKEN", "GITEA_CLONE_USERNAME", "GITEA_CLONE_URL", "GITEA_CLONE_REPOSITORY_ID",
		"GITEA_WEBHOOK_SECRET", "GITEA_GIT_PASSWORD", "GITEA_GIT_USERNAME", "GITHUB_TOKEN", "RUNNER_URL", "SECRET_ENCRYPTION_KEYS", "GITHUB_APP_PRIVATE_KEY", "GITHUB_APP_WEBHOOK_SECRET"} {
		t.Setenv(key, "")
	}
	if lane, hooks, err := workerRoutingConfig("local"); err != nil || lane != laneShared || !hooks {
		t.Fatalf("legacy shared worker: %q %t %v", lane, hooks, err)
	}
	t.Setenv("GITEA_WEBHOOK_SECRET", "synthetic")
	if _, _, err := workerRoutingConfig("local"); err == nil {
		t.Fatal("shared worker accepted webhook secret")
	}
	t.Setenv("DEV_PLANE_WORKER_LANE", laneWebhooks)
	if lane, hooks, err := workerRoutingConfig("local"); err != nil || lane != laneWebhooks || !hooks {
		t.Fatalf("webhook-only worker: %q %t %v", lane, hooks, err)
	}
	t.Setenv("GITEA_TOKEN", "synthetic-api")
	if _, _, err := workerRoutingConfig("local"); err == nil {
		t.Fatal("webhook worker accepted API credential")
	}
	t.Setenv("DEV_PLANE_WORKER_LANE", laneGitea)
	t.Setenv("GITEA_WEBHOOK_SECRET", "")
	t.Setenv("FORGE_PROVIDER", "gitea")
	t.Setenv("GITEA_URL", "http://127.0.0.1:1")
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	t.Setenv("GITEA_CLONE_TOKEN", "synthetic-clone")
	t.Setenv("GITEA_CLONE_USERNAME", "read-user")
	t.Setenv("GITEA_CLONE_URL", "http://127.0.0.1:1/acme/widget.git")
	t.Setenv("GITEA_CLONE_REPOSITORY_ID", "77")
	if lane, hooks, err := workerRoutingConfig("docker"); err != nil || lane != laneGitea || hooks {
		t.Fatalf("Gitea dedicated worker: %q %t %v", lane, hooks, err)
	}
	if _, _, err := workerRoutingConfig("local"); err == nil {
		t.Fatal("Gitea worker accepted local host workspace")
	}
	for _, key := range []string{"RUNNER_URL", "GITHUB_TOKEN", "GITEA_GIT_PASSWORD", "GITEA_GIT_USERNAME", "GITEA_WEBHOOK_SECRET", "DEV_PLANE_WORKER_WEBHOOKS", "SECRET_ENCRYPTION_KEYS", "GITHUB_APP_PRIVATE_KEY", "GITHUB_APP_WEBHOOK_SECRET"} {
		t.Setenv(key, "1")
		if _, _, err := workerRoutingConfig("docker"); err == nil {
			t.Fatalf("Gitea worker accepted %s", key)
		}
		t.Setenv(key, "")
	}
	t.Setenv("GITEA_BINDING_GENERATION", "")
	if _, _, err := workerRoutingConfig("docker"); err == nil {
		t.Fatal("Gitea worker accepted missing generation")
	}
	t.Setenv("DEV_PLANE_WORKER_LANE", "unknown")
	if _, _, err := workerRoutingConfig("docker"); err == nil {
		t.Fatal("unknown lane accepted")
	}
}

func routingID(kind, tenant int) string {
	return fmt.Sprintf("000000%02d-0000-4000-8000-%012d", kind, tenant)
}

func routingFixture(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "routing.db")
	external := os.Getenv("DEVPLANE_TEST_ROUTING_POSTGRES_URL")
	if external != "" {
		target, err := url.Parse(external)
		if err != nil || target.Scheme != "postgres" || target.Hostname() != "127.0.0.1" || target.Port() == "" ||
			target.User == nil || target.User.String() != "fixture" || target.Path != "/devplane_routing_test" ||
			target.RawQuery != "sslmode=disable" || target.Fragment != "" {
			t.Fatal("expected disposable loopback routing database")
		}
		dsn = external
	}
	database, err := db.New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	if external != "" {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM pg_tables WHERE schemaname='public'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("fixture must be empty: %d %v", count, err)
		}
	}
	if err := database.RunMigrations("../../../../packages/db/migrations"); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		org, user, project, repo, task, run := routingID(1, n), routingID(2, n), routingID(3, n), routingID(4, n), routingID(5, n), routingID(6, n)
		tag := fmt.Sprintf("fixture-%d", n)
		settings := "{}"
		if n == 2 {
			settings = "{\"forge\":{\"provider\":\"gitea\",\"base_url\":\"http://127.0.0.1:1\"}}"
		}
		routingExec(t, database.DB, "INSERT INTO organizations(id,name,slug) VALUES ($1,$2,$2)", org, tag)
		routingExec(t, database.DB, "INSERT INTO users(id,organization_id,email) VALUES ($1,$2,$3)", user, org, tag+"@example.test")
		routingExec(t, database.DB, "INSERT INTO projects(id,organization_id,name,slug) VALUES ($1,$2,$3,$3)", project, org, tag)
		routingExec(t, database.DB, "INSERT INTO repositories(id,project_id,owner,name,full_name,clone_url,settings) VALUES ($1,$2,'acme','widget','acme/widget','http://127.0.0.1:1/acme/widget.git',$3)", repo, project, settings)
		routingExec(t, database.DB, "INSERT INTO tasks(id,project_id,repository_id,created_by,title,status) VALUES ($1,$2,$3,$4,'fixture','approved')", task, project, repo, user)
		routingExec(t, database.DB, "INSERT INTO agent_runs(id,task_id,status) VALUES ($1,$2,'queued')", run, task)
		routingExec(t, database.DB, "INSERT INTO approvals(id,task_id,agent_run_id,approval_type,requested_by,response) VALUES ($1,$2,$3,'pr_create',$4,'approved'),($5,$2,$3,'pr_create',$4,'rejected')", routingID(7, n), task, run, user, routingID(8, n))
	}
	return database.DB
}

func routingExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func commandPayload(t *testing.T, subject string, tenant int) []byte {
	t.Helper()
	data := map[string]string{"task_id": routingID(5, tenant), "forge": "untrusted-override", "lane": "shared"}
	switch subject {
	case events.RunTriggered:
		data["run_id"] = routingID(6, tenant)
	case events.ApprovalApproved, events.ApprovalRejected:
		kind, response := 7, "approved"
		if subject == events.ApprovalRejected {
			kind, response = 8, "rejected"
		}
		data["approval_id"], data["agent_run_id"], data["approval_type"], data["response"] = routingID(kind, tenant), routingID(6, tenant), "pr_create", response
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

var routingCommands = []string{events.TaskApproved, events.RunTriggered, events.ApprovalApproved, events.ApprovalRejected}

func TestExecutionRoutingDurableIdentity(t *testing.T) {
	database := routingFixture(t)
	ctx := context.Background()
	for _, subject := range routingCommands {
		for tenant := 1; tenant <= 2; tenant++ {
			raw := commandPayload(t, subject, tenant)
			expected := subject
			if tenant == 2 {
				expected = "gitea.exec." + subject
			}
			got, err := events.ResolveExecutionSubject(ctx, database, subject, raw)
			if err != nil || got != expected {
				t.Fatalf("expected %s got %s %v", expected, got, err)
			}
			if err := events.VerifyExecutionDelivery(ctx, database, expected, raw, tenant == 2); err != nil {
				t.Fatal(err)
			}
			if err := events.VerifyExecutionDelivery(ctx, database, expected, raw, tenant != 2); !errors.Is(err, events.ErrExecutionRoute) {
				t.Fatal("wrong worker accepted command")
			}
		}
	}
	for _, tc := range []struct{ subject, key, value string }{
		{events.RunTriggered, "run_id", routingID(6, 1)},
		{events.ApprovalApproved, "agent_run_id", routingID(6, 1)},
		{events.ApprovalApproved, "approval_id", routingID(7, 1)},
		{events.ApprovalApproved, "approval_type", "agent_runtime"},
		{events.ApprovalApproved, "response", "rejected"},
	} {
		var payload map[string]string
		if err := json.Unmarshal(commandPayload(t, tc.subject, 2), &payload); err != nil {
			t.Fatal(err)
		}
		payload[tc.key] = tc.value
		raw, _ := json.Marshal(payload)
		if _, err := events.ResolveExecutionSubject(ctx, database, tc.subject, raw); !errors.Is(err, events.ErrExecutionRoute) {
			t.Fatalf("foreign or forged %s accepted: %v", tc.key, err)
		}
	}
	raw := commandPayload(t, events.TaskApproved, 2)
	routingExec(t, database, "UPDATE tasks SET project_id=$1 WHERE id=$2", routingID(3, 1), routingID(5, 2))
	if _, err := events.ResolveExecutionSubject(ctx, database, events.TaskApproved, raw); !errors.Is(err, events.ErrExecutionRoute) {
		t.Fatal("cross-project task was routed")
	}
	routingExec(t, database, "UPDATE tasks SET project_id=$1 WHERE id=$2", routingID(3, 2), routingID(5, 2))
	routingExec(t, database, "INSERT INTO gitea_repository_bindings(repository_id,project_id,organization_id,instance_url,upstream_repository_id,full_name) VALUES ($1,$2,$3,'http://127.0.0.1:1',77,'acme/widget')", routingID(4, 2), routingID(3, 2), routingID(1, 2))
	routingExec(t, database, "UPDATE repositories SET settings='{}' WHERE id=$1", routingID(4, 2))
	if _, err := events.ResolveExecutionSubject(ctx, database, events.TaskApproved, raw); !errors.Is(err, events.ErrExecutionRoute) {
		t.Fatal("reserved Gitea repository downgraded to shared")
	}
	routingExec(t, database, "UPDATE repositories SET deleted_at=CURRENT_TIMESTAMP WHERE id=$1", routingID(4, 1))
	if _, err := events.ResolveExecutionSubject(ctx, database, events.TaskApproved, commandPayload(t, events.TaskApproved, 1)); !errors.Is(err, events.ErrExecutionRoute) {
		t.Fatal("deleted repository was routed")
	}
	if _, err := events.ResolveExecutionSubject(ctx, nil, events.TaskApproved, raw); !errors.Is(err, events.ErrExecutionRoute) {
		t.Fatal("missing database did not fail closed")
	}
}

// Starts a private loopback NATS server, never the configured broker.
func routingTestBroker(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("DEVPLANE_TEST_NATS_SERVER")
	if binary == "" {
		t.Skip("set DEVPLANE_TEST_NATS_SERVER for disposable live broker qualification")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute nats-server path required")
	}
	dir := t.TempDir()
	logFile, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logFile.Close() })
	cmd := exec.Command(binary, "-a", "127.0.0.1", "-p", "-1", "-js", "-sd", filepath.Join(dir, "jetstream"), "--ports_file_dir", dir)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(dir, "*.ports"))
		if len(files) == 1 {
			raw, _ := os.ReadFile(files[0])
			var ports struct {
				Nats []string `json:"nats"`
			}
			if json.Unmarshal(raw, &ports) == nil && len(ports.Nats) == 1 {
				target, err := url.Parse(ports.Nats[0])
				if err != nil || target.Scheme != "nats" || target.Hostname() != "127.0.0.1" || target.Port() == "" || target.User != nil {
					t.Fatal("refusing non-loopback broker")
				}
				return target.String()
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("disposable broker did not start")
	return ""
}

func TestLiveRoutingUpgradeRetainsOldConsumers(t *testing.T) {
	addr := routingTestBroker(t)
	bus, err := events.New(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.Close()
	// Simulate an existing server prior to the new dedicated stream.
	for _, cfg := range events.DefaultStreamConfigs() {
		if cfg.Name == events.StreamGiteaExecution {
			continue
		}
		if _, err := bus.JetStream().AddStream(&cfg); err != nil {
			t.Fatal(err)
		}
	}
	_, err = bus.JetStream().AddConsumer(events.StreamTasks, &nats.ConsumerConfig{
		Durable: "tasks_star", DeliverSubject: nats.NewInbox(), FilterSubject: "tasks.*",
		AckPolicy: nats.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.CreateStreams(); err != nil {
		t.Fatalf("existing stream upgrade: %v", err)
	}
	if _, err := bus.GetStreamInfo(events.StreamGiteaExecution); err != nil {
		t.Fatal("new dedicated stream was not provisioned")
	}
	if info, err := bus.JetStream().ConsumerInfo(events.StreamTasks, "tasks_star"); err != nil ||
		info.Config.FilterSubject != "tasks.*" {
		t.Fatalf("existing shared consumer overwritten: %+v %v", info, err)
	}
	database := routingFixture(t)
	bus.WithExecutionRouting(database)
	received := make(chan string, 1)
	sub, err := bus.SubscribeManual("tasks.*", func(m *nats.Msg) {
		received <- m.Subject
		_ = m.Ack()
	})
	if err != nil {
		t.Fatalf("old durable cannot bind: %v", err)
	}
	defer sub.Unsubscribe()
	if err := bus.Publish(events.TaskApproved, commandPayload(t, events.TaskApproved, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case subject := <-received:
		if subject != events.TaskApproved {
			t.Fatalf("wrong legacy delivery: %s", subject)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy consumer did not receive shared task")
	}
}

func TestLiveWorkerCommandRouting(t *testing.T) {
	addr := routingTestBroker(t)
	database := routingFixture(t)
	open := func() *events.Bus {
		bus, err := events.New(addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bus.Close() })
		if err := bus.CreateStreams(); err != nil {
			t.Fatal(err)
		}
		return bus.WithExecutionRouting(database)
	}
	publisher, shared, gitea, hooks := open(), open(), open(), open()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	type delivery struct{ lane, subject, task string }
	deliveries := make(chan delivery, 32)
	receive := func(lane string) eventHandler {
		return func(m *nats.Msg) error {
			var payload events.TaskEvent
			if err := json.Unmarshal(m.Data, &payload); err != nil {
				return err
			}
			deliveries <- delivery{lane, m.Subject, payload.TaskID}
			return m.Ack()
		}
	}
	// Queue a command before the dedicated worker exists; the shared
	// subscriber must not consume it, nor should publishing fall back.
	offline := commandPayload(t, events.RunTriggered, 2)
	for _, pair := range []struct {
		lane string
		bus  *events.Bus
	}{{laneShared, shared}, {laneGitea, gitea}, {laneWebhooks, hooks}} {
		if pair.lane == laneGitea {
			if err := publisher.Publish(events.RunTriggered, offline); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-deliveries:
				t.Fatalf("offline Gitea work fell back to shared worker: %+v", got)
			case <-time.After(100 * time.Millisecond):
			}
			info, err := publisher.GetStreamInfo(events.StreamGiteaExecution)
			if err != nil || info.State.Msgs != 1 {
				t.Fatalf("offline command not queued: %+v %v", info, err)
			}
		}
		handle := receive(pair.lane)
		handlers := workerHandlers{taskCreated: handle, taskApproved: handle, taskCompleted: handle, taskFailed: handle, runTriggered: handle, runCompleted: handle, runFailed: handle, reviewCompleted: handle, approvalApproved: handle, approvalRejected: handle, approvalRequested: handle}
		if pair.lane == laneWebhooks {
			handlers = workerHandlers{webhook: handle}
		}
		shutdown := &shutdownContext{logger: logger}
		if err := registerWorkerSubscriptions(database, pair.bus, pair.lane, handlers, shutdown); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, sub := range shutdown.subscriptions {
				_ = sub.Unsubscribe()
			}
		})
	}
	expect := map[delivery]bool{}
	for _, subject := range routingCommands {
		for tenant := 1; tenant <= 2; tenant++ {
			if subject != events.RunTriggered || tenant != 2 {
				if err := publisher.Publish(subject, commandPayload(t, subject, tenant)); err != nil {
					t.Fatal(err)
				}
			}
			lane, expected := laneShared, subject
			if tenant == 2 {
				lane, expected = laneGitea, "gitea.exec."+subject
			}
			expect[delivery{lane, expected, routingID(5, tenant)}] = true
		}
	}
	for _, pair := range []struct{ subject, lane string }{{events.TaskCompleted, laneShared}, {events.AgentRunCompleted, laneShared}, {events.WebhookReceived, laneWebhooks}} {
		if err := publisher.Publish(pair.subject, commandPayload(t, pair.subject, 2)); err != nil {
			t.Fatal(err)
		}
		expect[delivery{pair.lane, pair.subject, routingID(5, 2)}] = true
	}
	timeout := time.After(10 * time.Second)
	for len(expect) > 0 {
		select {
		case got := <-deliveries:
			if !expect[got] {
				t.Fatalf("unexpected or duplicate dispatch: %+v", got)
			}
			delete(expect, got)
		case <-timeout:
			t.Fatalf("missing deliveries: %+v", expect)
		}
	}
	if err := publisher.Publish(events.GiteaRunTriggered, commandPayload(t, events.RunTriggered, 1)); !errors.Is(err, events.ErrExecutionRoute) {
		t.Fatal("direct route bypass accepted")
	}
	// Inject raw stale/malicious broker messages. Neither worker may invoke a handler.
	for _, pair := range []struct {
		subject string
		tenant  int
	}{{events.RunTriggered, 2}, {events.GiteaRunTriggered, 1}, {events.TaskApproved, 2}, {events.GiteaTaskApproved, 1}, {events.ApprovalApproved, 2}, {events.GiteaApprovalRejected, 1}} {
		base, _ := events.ExecutionCommand(pair.subject)
		if _, err := publisher.JetStream().Publish(pair.subject, commandPayload(t, base, pair.tenant)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case got := <-deliveries:
		t.Fatalf("misrouted event reached handler: %+v", got)
	case <-time.After(500 * time.Millisecond):
	}
	for _, stream := range []string{events.StreamTasks, events.StreamRuns, events.StreamGiteaExecution} {
		info, err := publisher.GetStreamInfo(stream)
		if err != nil || info.State.Msgs == 0 {
			t.Fatalf("misrouted message silently ACKed from %s: %+v %v", stream, info, err)
		}
	}
}
