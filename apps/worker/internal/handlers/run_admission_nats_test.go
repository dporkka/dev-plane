package handlers

import (
    "errors"
    "log/slog"
    "os"
    "strings"
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/nats-io/nats.go"
)

// This exercises the same JetStream message acknowledgement path as the worker.
// It is intentionally disabled in ordinary tests; CI supplies an isolated NATS.
func TestRunTriggeredJetStreamRedeliveryAfterDeniedAdmission(t *testing.T) {
    if os.Getenv("RUN_NATS_ADMISSION_INTEGRATION") != "1" {
        t.Skip("set RUN_NATS_ADMISSION_INTEGRATION=1 with running JetStream")
    }
    url := os.Getenv("NATS_URL")
    if url == "" { url = nats.DefaultURL }
    nc, err := nats.Connect(url)
    if err != nil { t.Fatal(err) }
    defer nc.Close()
    js, err := nc.JetStream()
    if err != nil { t.Fatal(err) }

    cases := []struct {
        name string
        initial RunAdmission
        delay time.Duration
    }{
        {name: "admission not configured", initial: nil, delay: 5 * time.Second},
        {name: "explicit admission denial", initial: &fakeRunAdmission{
            decision: RunAdmissionDecision{Allowed:false, Reason:"ownership-conflict", RetryAfter: 200*time.Millisecond},
        }, delay: 200*time.Millisecond},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            name := "ADMIT" + strings.ReplaceAll(uuid.NewString(), "-", "")
            subject := "admission.smoke." + uuid.NewString()
            _, err := js.AddStream(&nats.StreamConfig{
                Name: name,
                Subjects: []string{subject},
                Storage: nats.MemoryStorage,
                Retention: nats.WorkQueuePolicy,
            })
            if err != nil { t.Fatal(err) }
            defer func() { if err := js.DeleteStream(name); err != nil { t.Errorf("delete test stream: %v",err) } }()
            sub, err := js.SubscribeSync(subject, nats.ManualAck(), nats.AckWait(15*time.Second))
            if err != nil { t.Fatal(err) }
            defer sub.Unsubscribe()

            executor := &fakeRunExecutor{}
            handler := NewRunHandler(nil, slog.Default(), nil).WithRunExecutor(executor)
            if tc.initial != nil { handler.WithRunAdmission(tc.initial) }
            _, err = js.Publish(subject, []byte(`{"run_id":"run-jetstream","task_id":"task-jetstream"}`))
            if err != nil { t.Fatal(err) }

            first, err := sub.NextMsg(3*time.Second)
            if err != nil { t.Fatalf("first delivery: %v",err) }
            if err := handler.HandleRunTriggered(first); !errors.Is(err,ErrRunAdmissionDeferred) {
                t.Fatalf("denial must defer without ack: %v",err)
            }
            if executor.runID != "" { t.Fatalf("executor invoked before admission: %q",executor.runID) }
            handler.WithRunAdmission(&fakeRunAdmission{decision:RunAdmissionDecision{Allowed:true}})

            second, err := sub.NextMsg(tc.delay+4*time.Second)
            if err != nil { t.Fatalf("no NAK redelivery after %v: %v",tc.delay,err) }
            meta, err := second.Metadata()
            if err != nil { t.Fatal(err) }
            if meta.NumDelivered != 2 { t.Fatalf("redelivery count %d, want 2",meta.NumDelivered) }
            if err := handler.HandleRunTriggered(second); err != nil { t.Fatalf("allowed second delivery: %v",err) }
            if executor.runID != "run-jetstream" { t.Fatalf("allowed run was not executed: %q",executor.runID) }
            if _, err := sub.NextMsg(750*time.Millisecond); !errors.Is(err,nats.ErrTimeout) {
                t.Fatalf("message delivered again after ACK: %v",err)
            }
        })
    }
}
