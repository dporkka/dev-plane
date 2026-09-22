package server

import (
	"database/sql"
	"fmt"
	"strings"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	"github.com/ai-dev-control-plane/api/internal/config"
)

func newArtifactServices(cfg *config.Config, database *sql.DB) (*artifactstore.Manager, artifactstore.LeaseStore, error) {
	var (
		store artifactstore.BlobStore
		err   error
	)

	switch strings.ToLower(strings.TrimSpace(cfg.ArtifactStore)) {
	case "", "local":
		store, err = artifactstore.NewLocalStore(cfg.ArtifactsDir)
	case "s3", "r2":
		store, err = artifactstore.NewS3Store(artifactstore.S3StoreConfig{
			Endpoint:        cfg.ArtifactS3Endpoint,
			Region:          cfg.ArtifactS3Region,
			Bucket:          cfg.ArtifactS3Bucket,
			Prefix:          cfg.ArtifactS3Prefix,
			AccessKeyID:     cfg.ArtifactS3AccessKey,
			SecretAccessKey: cfg.ArtifactS3SecretKey,
			SessionToken:    cfg.ArtifactS3Token,
		})
	default:
		return nil, nil, fmt.Errorf("unsupported ARTIFACT_STORE %q", cfg.ArtifactStore)
	}
	if err != nil {
		return nil, nil, err
	}

	manager, err := artifactstore.NewManager(store)
	if err != nil {
		return nil, nil, err
	}
	dialect := artifactstore.SQLDialectSQLite
	if strings.Contains(strings.ToLower(cfg.DatabaseURL), "postgres") {
		dialect = artifactstore.SQLDialectPostgres
	}
	leases, err := artifactstore.NewSQLLeaseStore(database, dialect)
	if err != nil {
		return nil, nil, err
	}
	return manager, leases, nil
}
