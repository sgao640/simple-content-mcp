package mcpserver

import (
	"github.com/tendant/simple-content/pkg/simplecontent"
	"github.com/tendant/simple-content/pkg/simplecontent/admin"
	"github.com/tendant/simple-content-mcp/pkg/mcpserver/auth"
)

// TransportMode defines the MCP transport protocol
type TransportMode string

const (
	// TransportStdio uses standard input/output for MCP communication
	TransportStdio TransportMode = "stdio"
	// TransportSSE uses Server-Sent Events for MCP communication (future)
	TransportSSE TransportMode = "sse"
	// TransportHTTP uses HTTP for MCP communication (future)
	TransportHTTP TransportMode = "http"
)

// Config holds server configuration
type Config struct {
	// Core dependencies
	Service        simplecontent.Service
	StorageService simplecontent.StorageService // Required for object operations and upload URLs
	AdminService   admin.AdminService           // Optional: for admin operations (list all content, etc.)

	// Server settings
	Name    string
	Version string

	// Transport settings
	Mode    TransportMode // stdio, sse, http
	Host    string        // For SSE/HTTP modes
	Port    int           // For SSE/HTTP modes
	BaseURL string        // For SSE mode

	// Behavior settings
	MaxBatchSize    int // Maximum number of items in batch operations
	DefaultPageSize int // Default page size for list operations
	MaxPageSize     int // Maximum page size for list operations

	// Feature flags
	EnableResources bool // Enable MCP resources (Phase 3)
	EnablePrompts   bool // Enable MCP prompts (Phase 3)

	// List content settings
	RequireOwnerID bool // Require owner_id for list_content tool

	// Authentication settings (Phase 5)
	AuthEnabled   bool               // Enable authentication
	Authenticator auth.Authenticator // Authenticator implementation
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig(service simplecontent.Service) Config {
	return Config{
		Service:         service,
		Name:            "simple-content-mcp",
		Version:         "0.1.0",
		Mode:            TransportStdio,
		Host:            "localhost",
		Port:            8080,
		MaxBatchSize:    100,
		DefaultPageSize: 50,
		MaxPageSize:     1000,
		EnableResources: true,  // Phase 3
		EnablePrompts:   true,  // Phase 3
		RequireOwnerID:  true,  // Require owner_id for list_content by default
		AuthEnabled:     false, // Phase 5 - disabled by default
		Authenticator:   nil,   // Phase 5 - must be set if AuthEnabled
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Service == nil {
		return &ConfigError{Field: "Service", Message: "service is required"}
	}

	if c.StorageService == nil {
		return &ConfigError{Field: "StorageService", Message: "storage service is required"}
	}

	if c.Name == "" {
		return &ConfigError{Field: "Name", Message: "name is required"}
	}

	if c.Version == "" {
		return &ConfigError{Field: "Version", Message: "version is required"}
	}

	if c.MaxBatchSize <= 0 {
		return &ConfigError{Field: "MaxBatchSize", Message: "must be greater than 0"}
	}

	if c.DefaultPageSize <= 0 {
		return &ConfigError{Field: "DefaultPageSize", Message: "must be greater than 0"}
	}

	if c.MaxPageSize <= 0 {
		return &ConfigError{Field: "MaxPageSize", Message: "must be greater than 0"}
	}

	if c.DefaultPageSize > c.MaxPageSize {
		return &ConfigError{Field: "DefaultPageSize", Message: "cannot be greater than MaxPageSize"}
	}

	// Validate authentication configuration
	if c.AuthEnabled && c.Authenticator == nil {
		return &ConfigError{Field: "Authenticator", Message: "authenticator is required when AuthEnabled is true"}
	}

	return nil
}

// ConfigError represents a configuration validation error
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return "config error: " + e.Field + ": " + e.Message
}
