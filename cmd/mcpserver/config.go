package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tendant/simple-content-mcp/pkg/mcpserver"
	"github.com/tendant/simple-content-mcp/pkg/mcpserver/auth"
	"github.com/tendant/simple-content/pkg/simplecontent"
	memoryrepo "github.com/tendant/simple-content/pkg/simplecontent/repo/memory"
	postgresrepo "github.com/tendant/simple-content/pkg/simplecontent/repo/postgres"
	fsstorage "github.com/tendant/simple-content/pkg/simplecontent/storage/fs"
	memorystorage "github.com/tendant/simple-content/pkg/simplecontent/storage/memory"
)

// LoadConfigFromEnv creates a server configuration from environment variables
func LoadConfigFromEnv(service simplecontent.Service, storageService simplecontent.StorageService) mcpserver.Config {
	config := mcpserver.DefaultConfig(service)
	config.StorageService = storageService

	// Transport mode
	if mode := os.Getenv("MCP_MODE"); mode != "" {
		config.Mode = mcpserver.TransportMode(mode)
	}

	// Host and port
	if host := os.Getenv("MCP_HOST"); host != "" {
		config.Host = host
	}
	if portStr := os.Getenv("MCP_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			config.Port = port
		}
	}

	// Base URL for SSE mode
	if baseURL := os.Getenv("MCP_BASE_URL"); baseURL != "" {
		config.BaseURL = baseURL
	}

	// Batch and pagination settings
	if sizeStr := os.Getenv("MCP_MAX_BATCH_SIZE"); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil {
			config.MaxBatchSize = size
		}
	}
	if sizeStr := os.Getenv("MCP_DEFAULT_PAGE_SIZE"); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil {
			config.DefaultPageSize = size
		}
	}
	if sizeStr := os.Getenv("MCP_MAX_PAGE_SIZE"); sizeStr != "" {
		if size, err := strconv.Atoi(sizeStr); err == nil {
			config.MaxPageSize = size
		}
	}

	// Feature flags
	if resourcesStr := os.Getenv("MCP_ENABLE_RESOURCES"); resourcesStr != "" {
		if enabled, err := strconv.ParseBool(resourcesStr); err == nil {
			config.EnableResources = enabled
		}
	}
	if promptsStr := os.Getenv("MCP_ENABLE_PROMPTS"); promptsStr != "" {
		if enabled, err := strconv.ParseBool(promptsStr); err == nil {
			config.EnablePrompts = enabled
		}
	}

	// List content settings
	if requireOwnerIDStr := os.Getenv("MCP_REQUIRE_OWNER_ID"); requireOwnerIDStr != "" {
		if required, err := strconv.ParseBool(requireOwnerIDStr); err == nil {
			config.RequireOwnerID = required
		}
	}

	// Authentication
	if authStr := os.Getenv("MCP_AUTH_ENABLED"); authStr != "" {
		if enabled, err := strconv.ParseBool(authStr); err == nil {
			config.AuthEnabled = enabled
			if enabled {
				config.Authenticator = loadAuthenticator()
			}
		}
	}

	return config
}

// loadAuthenticator creates an authenticator from environment variables
func loadAuthenticator() auth.Authenticator {
	authenticator := auth.NewAPIKeyAuthenticator()

	// Load API keys from environment
	// Format: MCP_API_KEY_1=key:owner_id:tenant_id:expires_at
	// Example: MCP_API_KEY_1=mykey123:550e8400-e29b-41d4-a716-446655440000::
	for i := 1; i <= 10; i++ {
		keyEnv := os.Getenv(fmt.Sprintf("MCP_API_KEY_%d", i))
		if keyEnv == "" {
			continue
		}

		keyInfo := parseAPIKeyEnv(keyEnv)
		if keyInfo != nil {
			authenticator.AddKey(keyInfo)
		}
	}

	return authenticator
}

// parseAPIKeyEnv parses an API key environment variable
// Format: key:owner_id:tenant_id:expires_at
func parseAPIKeyEnv(value string) *auth.KeyInfo {
	parts := strings.SplitN(value, ":", 4)
	if len(parts) < 2 {
		return nil
	}

	keyInfo := &auth.KeyInfo{
		Key: parts[0],
	}

	// Parse owner ID
	if ownerID, err := uuid.Parse(parts[1]); err == nil {
		keyInfo.OwnerID = ownerID
	} else {
		return nil // Owner ID is required
	}

	// Parse optional tenant ID
	if len(parts) > 2 && parts[2] != "" {
		if tenantID, err := uuid.Parse(parts[2]); err == nil {
			keyInfo.TenantID = tenantID
		}
	}

	// Parse optional expiration
	if len(parts) > 3 && parts[3] != "" {
		if expiresAt, err := time.Parse(time.RFC3339, parts[3]); err == nil {
			keyInfo.ExpiresAt = &expiresAt
		}
	}

	return keyInfo
}

// CreateServiceFromEnv creates a simple-content service from environment variables
// Supports multiple backends:
// - Repository: memory, postgres (via DATABASE_URL)
// - Storage: memory, fs (filesystem via STORAGE_PATH), s3 (requires AWS SDK)
// Returns both service and repository (repository is needed for admin operations)
func CreateServiceFromEnv() (simplecontent.Service, simplecontent.StorageService, simplecontent.Repository, error) {
	ctx := context.Background()

	// Configuration
	databaseURL := os.Getenv("DATABASE_URL")
	storageBackend := getEnvOrDefault("STORAGE_BACKEND", "memory")
	storagePath := os.Getenv("STORAGE_PATH")

	// Create repository
	var repo simplecontent.Repository
	var err error

	if databaseURL != "" {
		// PostgreSQL repository
		pool, err := pgxpool.New(ctx, databaseURL)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
		}

		// Test connection
		if err := pool.Ping(ctx); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
		}

		repo = postgresrepo.New(pool)
	} else {
		// In-memory repository (default)
		repo = memoryrepo.New()
	}

	// Create storage backend
	var store simplecontent.BlobStore

	switch storageBackend {
	case "fs", "filesystem":
		// Filesystem storage
		if storagePath == "" {
			storagePath = "./data/storage"
		}
		store, err = fsstorage.New(fsstorage.Config{
			BaseDir:   storagePath,
			URLPrefix: getEnvOrDefault("STORAGE_URL_PREFIX", ""),
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to create filesystem storage: %w", err)
		}

	case "s3":
		// S3 storage - requires AWS SDK dependencies
		return nil, nil, nil, fmt.Errorf("s3 storage is not implemented yet")

	case "memory":
		// In-memory storage (default)
		store = memorystorage.New()

	default:
		return nil, nil, nil, fmt.Errorf("unknown storage backend: %s", storageBackend)
	}

	// Create service
	service, err := simplecontent.New(
		simplecontent.WithRepository(repo),
		simplecontent.WithBlobStore("default", store),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create service: %w", err)
	}

	// Service itself implements StorageService
	storageService, ok := service.(simplecontent.StorageService)
	if !ok {
		return nil, nil, nil, fmt.Errorf("service does not implement StorageService")
	}

	return service, storageService, repo, nil
}

// getEnvOrDefault returns environment variable value or default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
