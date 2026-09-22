package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	"github.com/ai-dev-control-plane/worker/internal/handlers"
)

func initArtifactManager() (*artifactstore.Manager, error) {
	backend := strings.ToLower(strings.TrimSpace(envOrDefault("ARTIFACT_STORE", "local")))
	var (
		store artifactstore.BlobStore
		err   error
	)
	switch backend {
	case "", "local":
		store, err = artifactstore.NewLocalStore(envOrDefault("ARTIFACTS_DIR", "./artifacts/cas"))
	case "s3", "r2":
		store, err = artifactstore.NewS3Store(artifactstore.S3StoreConfig{
			Endpoint:        os.Getenv("ARTIFACT_S3_ENDPOINT"),
			Region:          envOrDefault("ARTIFACT_S3_REGION", "auto"),
			Bucket:          os.Getenv("ARTIFACT_S3_BUCKET"),
			Prefix:          envOrDefault("ARTIFACT_S3_PREFIX", "cas"),
			AccessKeyID:     os.Getenv("ARTIFACT_S3_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("ARTIFACT_S3_SECRET_ACCESS_KEY"),
			SessionToken:    os.Getenv("ARTIFACT_S3_SESSION_TOKEN"),
		})
	default:
		return nil, fmt.Errorf("unsupported ARTIFACT_STORE %q", backend)
	}
	if err != nil {
		return nil, err
	}
	return artifactstore.NewManager(store)
}

func startArtifactBlockerReconciler(ctx context.Context, handler *handlers.RunHandler, logger *slog.Logger) {
	if handler == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				err := handler.ResumeArtifactBlockedRuns(reconcileCtx)
				cancel()
				if err != nil {
					logger.Warn("artifact blocker reconciliation failed", "error", err)
				}
			}
		}
	}()
}

func startArtifactUploadReconciler(
	ctx context.Context,
	reconciler *handlers.ArtifactUploadReconciler,
	logger *slog.Logger,
) {
	if reconciler == nil {
		return
	}
	go func() {
		run := func() {
			if err := reconciler.Reconcile(ctx); err != nil {
				logger.Warn("artifact upload reconciliation failed", "error", err)
			}
		}

		run()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
