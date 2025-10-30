package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/tendant/simple-content-mcp/pkg/mcpserver"
	"github.com/tendant/simple-content/pkg/simplecontent"
	"github.com/tendant/simple-content/pkg/simplecontent/admin"
	memoryrepo "github.com/tendant/simple-content/pkg/simplecontent/repo/memory"
	memorystorage "github.com/tendant/simple-content/pkg/simplecontent/storage/memory"
)

func main() {
	// Load .env file if it exists (ignore error if file doesn't exist)
	if err := godotenv.Load(); err != nil {
		// Only log if error is not "file not found"
		if !os.IsNotExist(err) {
			log.Printf("Warning: Error loading .env file: %v", err)
		}
	}

	// Parse command-line flags
	var (
		mode    = flag.String("mode", "stdio", "Transport mode: stdio, sse, http")
		port    = flag.Int("port", 8080, "Port for SSE/HTTP mode")
		version = flag.Bool("version", false, "Print version and exit")
		envFile = flag.String("env", "", "Path to .env file (default: .env in current directory)")
	)
	flag.Parse()

	// Load custom .env file if specified
	if *envFile != "" {
		if err := godotenv.Load(*envFile); err != nil {
			log.Fatalf("Error loading env file %s: %v", *envFile, err)
		}
		log.Printf("Loaded configuration from %s", *envFile)
	}

	if *version {
		fmt.Println("simple-content-mcp v0.1.0")
		os.Exit(0)
	}

	// Create context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, shutting down gracefully...")
		cancel()
	}()

	// Initialize simple-content service
	// Try to load from environment first, fallback to in-memory
	service, storageService, repo, err := CreateServiceFromEnv()
	if err != nil {
		log.Printf("Warning: Failed to load service from environment: %v", err)
		log.Println("Falling back to in-memory service...")
		service, storageService, repo, err = createService()
		if err != nil {
			log.Fatalf("Failed to create service: %v", err)
		}
	}

	// Create MCP server configuration from environment
	config := LoadConfigFromEnv(service, storageService)

	// Create admin service if repository is available
	if repo != nil {
		config.AdminService = admin.New(repo)
	}

	// Override with command-line flags if provided
	if *mode != "stdio" {
		config.Mode = mcpserver.TransportMode(*mode)
	}
	if *port != 8080 {
		config.Port = *port
	}

	// Create and start MCP server
	server, err := mcpserver.New(config)
	if err != nil {
		log.Fatalf("Failed to create MCP server: %v", err)
	}

	log.Printf("Starting MCP server in %s mode...", config.Mode)
	if err := server.Serve(ctx); err != nil {
		if err == context.Canceled {
			log.Println("Server stopped")
		} else {
			log.Fatalf("Server error: %v", err)
		}
	}
}

// createService creates a simple-content service with in-memory storage
// This is suitable for development and testing
func createService() (simplecontent.Service, simplecontent.StorageService, simplecontent.Repository, error) {
	// Create in-memory repository
	repo := memoryrepo.New()

	// Create in-memory blob storage
	blobStore := memorystorage.New()

	// Create service with memory backends
	service, err := simplecontent.New(
		simplecontent.WithRepository(repo),
		simplecontent.WithBlobStore("default", blobStore),
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
