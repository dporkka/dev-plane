package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	externalCallbackURLEnv    = "EXTERNAL_TASK_CALLBACK_URL"
	externalCallbackSecretEnv = "EXTERNAL_TASK_CALLBACK_SECRET"
)

type externalCallbackOptions struct {
	EventID   string
	EventType string
	RunID     string
	Status    string
	Artifact  map[string]any
	Error     any
}

// notifyExternalTaskCallback sends a signed callback when both the worker and
// task spec opt in. The destination and secret are server configuration, not
// task input, preventing task data from becoming an arbitrary HTTP target.
func notifyExternalTaskCallback(
	ctx context.Context,
	db *sql.DB,
	logger *slog.Logger,
	taskID string,
	opts externalCallbackOptions,
) error {
	if db == nil || strings.TrimSpace(taskID) == "" || strings.TrimSpace(opts.EventType) == "" {
		return nil
	}

	callbackURL := strings.TrimSpace(os.Getenv(externalCallbackURLEnv))
	secret := strings.TrimSpace(os.Getenv(externalCallbackSecretEnv))
	if callbackURL == "" && secret == "" {
		// Feature is not configured on this worker. Avoid adding a database query
		// to every task lifecycle event in installations that do not use callbacks.
		return nil
	}
	if callbackURL == "" || secret == "" {
		return fmt.Errorf("external callback configuration requires both %s and %s", externalCallbackURLEnv, externalCallbackSecretEnv)
	}

	spec, enabled, err := loadExternalCallbackSpec(ctx, db, taskID, opts.EventType)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	eventID := strings.TrimSpace(opts.EventID)
	if eventID == "" {
		eventID = fmt.Sprintf("dev-plane:task:%s:%s", taskID, opts.EventType)
	}
	payload := map[string]any{
		"event_id":   eventID,
		"event_type": opts.EventType,
		"task_id":    taskID,
		"source":     spec.Source,
		"source_id":  spec.SourceID,
		"slug":       spec.Slug,
		"status":     opts.Status,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}
	if opts.RunID != "" {
		payload["run_id"] = opts.RunID
	}
	if len(opts.Artifact) > 0 {
		payload["artifact"] = opts.Artifact
	}
	if opts.Error != nil {
		payload["error"] = opts.Error
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal external callback: %w", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := signExternalCallback(secret, timestamp, body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create external callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dev-plane/external-callback")
	req.Header.Set("X-Dev-Plane-Timestamp", timestamp)
	req.Header.Set("X-Dev-Plane-Signature", signature)

	timeout := 5 * time.Second
	if raw := strings.TrimSpace(os.Getenv("EXTERNAL_TASK_CALLBACK_TIMEOUT_SECONDS")); raw != "" {
		if seconds, parseErr := strconv.ParseFloat(raw, 64); parseErr == nil && seconds > 0 {
			timeout = time.Duration(seconds * float64(time.Second))
		}
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send external callback: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("external callback returned HTTP %d", resp.StatusCode)
	}
	if logger != nil {
		logger.Info("external task callback delivered", "task_id", taskID, "event_type", opts.EventType, "event_id", eventID)
	}
	return nil
}

type externalCallbackSpec struct {
	Source   string
	SourceID string
	Slug     string
}

func loadExternalCallbackSpec(ctx context.Context, db *sql.DB, taskID, eventType string) (externalCallbackSpec, bool, error) {
	var rawSpec sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT spec FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&rawSpec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return externalCallbackSpec{}, false, nil
		}
		return externalCallbackSpec{}, false, fmt.Errorf("load task callback spec: %w", err)
	}
	if !rawSpec.Valid || strings.TrimSpace(rawSpec.String) == "" {
		return externalCallbackSpec{}, false, nil
	}

	var spec map[string]any
	if err := json.Unmarshal([]byte(rawSpec.String), &spec); err != nil {
		return externalCallbackSpec{}, false, fmt.Errorf("decode task callback spec: %w", err)
	}
	callback, ok := spec["callback"].(map[string]any)
	if !ok {
		return externalCallbackSpec{}, false, nil
	}
	enabled, _ := callback["enabled"].(bool)
	if !enabled || !callbackEventAllowed(callback, eventType) {
		return externalCallbackSpec{}, false, nil
	}

	result := externalCallbackSpec{
		Source:   stringValue(spec["source"]),
		SourceID: stringValue(spec["source_id"]),
	}
	if apiFactory, ok := spec["api_factory"].(map[string]any); ok {
		result.Slug = stringValue(apiFactory["slug"])
	}
	return result, true, nil
}

func callbackEventAllowed(callback map[string]any, eventType string) bool {
	rawEvents, exists := callback["events"]
	if !exists {
		return true
	}
	events, ok := rawEvents.([]any)
	if !ok {
		return false
	}
	for _, event := range events {
		if stringValue(event) == eventType {
			return true
		}
	}
	return false
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func signExternalCallback(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
