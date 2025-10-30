package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tendant/simple-content/pkg/simplecontent"
	memoryrepo "github.com/tendant/simple-content/pkg/simplecontent/repo/memory"
	memorystorage "github.com/tendant/simple-content/pkg/simplecontent/storage/memory"
	s3storage "github.com/tendant/simple-content/pkg/simplecontent/storage/s3"
)

// createTestService creates a service with in-memory backends for testing
func createTestService(t *testing.T) simplecontent.Service {
	repo := memoryrepo.New()
	blobStore := memorystorage.New()

	service, err := simplecontent.New(
		simplecontent.WithRepository(repo),
		simplecontent.WithBlobStore("default", blobStore),
	)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	return service
}

// createTestServer creates a server for testing
func createTestServer(t *testing.T) *Server {
	service := createTestService(t)
	config := DefaultConfig(service)
	config.StorageService = service.(simplecontent.StorageService) // Service implements StorageService

	server, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	return server
}

// createS3TestService creates a service with S3 backend by loading .env.test
// Skips the test if S3/MinIO is not configured
func createS3TestService(t *testing.T) simplecontent.Service {
	// Load .env.test file
	if err := godotenv.Load("../../.env.test"); err != nil {
		t.Skip("Skipping S3 test: .env.test not found")
	}

	// Check if S3 is configured
	if os.Getenv("STORAGE_BACKEND") != "s3" {
		t.Skip("Skipping S3 test: STORAGE_BACKEND is not s3")
	}

	// Read S3 configuration from environment
	s3Config := s3storage.Config{
		Region:                 os.Getenv("AWS_REGION"),
		Bucket:                 os.Getenv("AWS_S3_BUCKET"),
		AccessKeyID:            os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey:        os.Getenv("AWS_SECRET_ACCESS_KEY"),
		Endpoint:               os.Getenv("AWS_S3_ENDPOINT"),
		UseSSL:                 false,
		UsePathStyle:           true,
		PresignDuration:        3600,
		CreateBucketIfNotExist: true,
	}

	// Create S3 storage backend
	s3Store, err := s3storage.New(s3Config)
	if err != nil {
		t.Fatalf("Failed to create S3 storage: %v", err)
	}

	// Create memory repository
	repo := memoryrepo.New()

	// Create service with S3 backend
	service, err := simplecontent.New(
		simplecontent.WithRepository(repo),
		simplecontent.WithBlobStore("default", s3Store),
	)
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	return service
}

// createS3TestServer creates a server with S3 backend for testing
func createS3TestServer(t *testing.T) *Server {
	service := createS3TestService(t)
	config := DefaultConfig(service)
	config.StorageService = service.(simplecontent.StorageService)

	server, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	return server
}

func TestServerCreation(t *testing.T) {
	service := createTestService(t)
	config := DefaultConfig(service)
	config.StorageService = service.(simplecontent.StorageService)

	server, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	if server == nil {
		t.Fatal("Server is nil")
	}

	if server.service == nil {
		t.Fatal("Service is nil")
	}

	if server.storageService == nil {
		t.Fatal("Storage service is nil")
	}

	if server.mcpServer == nil {
		t.Fatal("MCP server is nil")
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		wantError bool
	}{
		{
			name: "valid config",
			config: func() Config {
				svc := createTestService(t)
				return Config{
					Service:         svc,
					StorageService:  svc.(simplecontent.StorageService),
					Name:            "test-server",
					Version:         "0.1.0",
					Mode:            TransportStdio,
					MaxBatchSize:    100,
					DefaultPageSize: 50,
					MaxPageSize:     1000,
				}
			}(),
			wantError: false,
		},
		{
			name: "missing service",
			config: Config{
				Name:            "test-server",
				Version:         "0.1.0",
				MaxBatchSize:    100,
				DefaultPageSize: 50,
				MaxPageSize:     1000,
			},
			wantError: true,
		},
		{
			name: "missing storage service",
			config: Config{
				Service:         createTestService(t),
				Name:            "test-server",
				Version:         "0.1.0",
				MaxBatchSize:    100,
				DefaultPageSize: 50,
				MaxPageSize:     1000,
			},
			wantError: true,
		},
		{
			name: "missing name",
			config: func() Config {
				svc := createTestService(t)
				return Config{
					Service:         svc,
					StorageService:  svc.(simplecontent.StorageService),
					Version:         "0.1.0",
					MaxBatchSize:    100,
					DefaultPageSize: 50,
					MaxPageSize:     1000,
				}
			}(),
			wantError: true,
		},
		{
			name: "invalid page size",
			config: func() Config {
				svc := createTestService(t)
				return Config{
					Service:         svc,
					StorageService:  svc.(simplecontent.StorageService),
					Name:            "test-server",
					Version:         "0.1.0",
					MaxBatchSize:    100,
					DefaultPageSize: 100,
					MaxPageSize:     50,
				}
			}(),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestUploadContentTool(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()
	testData := "Hello, World!"
	encodedData := base64.StdEncoding.EncodeToString([]byte(testData))

	args := map[string]interface{}{
		"owner_id":  ownerID.String(),
		"name":      "test.txt",
		"data":      encodedData,
		"file_name": "test.txt",
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "upload_content",
			Arguments: argsJSON,
		},
	}

	result, err := server.handleUploadContent(ctx, req)
	if err != nil {
		t.Fatalf("handleUploadContent failed: %v", err)
	}

	if result == nil {
		t.Fatal("Result is nil")
	}

	if len(result.Content) == 0 {
		t.Fatal("Result content is empty")
	}

	// Verify the result contains an ID
	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("Result content is not TextContent")
	}

	var resultData map[string]interface{}
	if err := json.Unmarshal([]byte(textContent.Text), &resultData); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if resultData["id"] == nil {
		t.Fatal("Result does not contain ID")
	}

	if resultData["status"] != "uploaded" {
		t.Errorf("Expected status 'uploaded', got %v", resultData["status"])
	}
}

func TestCreateUploadTool(t *testing.T) {
	server := createS3TestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	args := map[string]interface{}{
		"owner_id":      ownerID.String(),
		"name":          "test-async.txt",
		"description":   "Test async upload",
		"document_type": "text/plain",
		"file_name":     "test-async.txt",
		"tags":          []string{"test", "async"},
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Failed to marshal args: %v", err)
	}

	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "create_upload",
			Arguments: argsJSON,
		},
	}

	result, err := server.handleCreateUpload(ctx, req)
	if err != nil {
		t.Fatalf("handleCreateUpload failed: %v", err)
	}

	if result == nil {
		t.Fatal("Result is nil")
	}

	if len(result.Content) == 0 {
		t.Fatal("Result content is empty")
	}

	// Verify the result contains expected fields
	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatal("Result content is not TextContent")
	}

	var resultData map[string]interface{}
	if err := json.Unmarshal([]byte(textContent.Text), &resultData); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	// Verify content_id is present and valid
	contentID, ok := resultData["content_id"].(string)
	if !ok || contentID == "" {
		t.Fatal("Result does not contain valid content_id")
	}

	// Verify upload_url is present
	uploadURL, ok := resultData["upload_url"].(string)
	if !ok || uploadURL == "" {
		t.Fatal("Result does not contain valid upload_url")
	}

	// Verify status is "created"
	if resultData["status"] != "created" {
		t.Errorf("Expected status 'created', got %v", resultData["status"])
	}

	// Verify created_at is present
	if resultData["created_at"] == nil {
		t.Error("Result does not contain created_at")
	}

	// Verify the content was actually created by fetching it
	getArgs := map[string]interface{}{
		"content_id": contentID,
	}

	getArgsJSON, _ := json.Marshal(getArgs)
	getReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "get_content",
			Arguments: getArgsJSON,
		},
	}

	getResult, err := server.handleGetContent(ctx, getReq)
	if err != nil {
		t.Fatalf("handleGetContent failed: %v", err)
	}

	getTextContent := getResult.Content[0].(*mcp.TextContent)
	var getResultData map[string]interface{}
	json.Unmarshal([]byte(getTextContent.Text), &getResultData)

	if getResultData["id"] != contentID {
		t.Errorf("Expected content_id %s, got %v", contentID, getResultData["content_id"])
	}

	if getResultData["status"] != "created" {
		t.Errorf("Expected status 'created', got %v", getResultData["status"])
	}
}

func TestUploadDoneTool(t *testing.T) {
	server := createS3TestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Step 1: Create content with create_upload
	createArgs := map[string]interface{}{
		"owner_id":      ownerID.String(),
		"name":          "test-upload-done.txt",
		"description":   "Test upload done workflow",
		"document_type": "text/plain",
		"file_name":     "test-upload-done.txt",
	}

	createArgsJSON, _ := json.Marshal(createArgs)
	createReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "create_upload",
			Arguments: createArgsJSON,
		},
	}

	createResult, err := server.handleCreateUpload(ctx, createReq)
	if err != nil {
		t.Fatalf("handleCreateUpload failed: %v", err)
	}

	// Extract content_id and upload_url
	createTextContent := createResult.Content[0].(*mcp.TextContent)
	var createData map[string]interface{}
	json.Unmarshal([]byte(createTextContent.Text), &createData)
	contentID := createData["content_id"].(string)
	uploadURL := createData["upload_url"].(string)

	// Verify we got an upload URL
	if uploadURL == "" {
		t.Fatal("No upload URL returned")
	}

	// Step 2: Simulate file upload to S3
	// For now, we'll use the service directly to upload the object
	// In a real scenario, the client would PUT to the uploadURL
	testData := []byte("Hello, this is test content for upload_done!")

	// We need to upload via the storage service
	contentUUID, _ := uuid.Parse(contentID)
	objects, _ := server.service.GetObjectsByContentID(ctx, contentUUID)
	if len(objects) > 0 {
		uploadReq := simplecontent.UploadObjectRequest{
			ObjectID: objects[0].ID,
			Reader:   strings.NewReader(string(testData)),
			MimeType: "text/plain",
		}
		if err := server.storageService.UploadObject(ctx, uploadReq); err != nil {
			t.Fatalf("Failed to upload object: %v", err)
		}
	}

	// Step 3: Call upload_done
	doneArgs := map[string]interface{}{
		"content_id": contentID,
	}

	doneArgsJSON, _ := json.Marshal(doneArgs)
	doneReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "upload_done",
			Arguments: doneArgsJSON,
		},
	}

	doneResult, err := server.handleUploadDone(ctx, doneReq)
	if err != nil {
		t.Fatalf("handleUploadDone failed: %v", err)
	}

	// Verify the result
	doneTextContent := doneResult.Content[0].(*mcp.TextContent)
	var doneData map[string]interface{}
	json.Unmarshal([]byte(doneTextContent.Text), &doneData)

	// Verify status changed to "uploaded"
	if doneData["status"] != "uploaded" {
		t.Errorf("Expected status 'uploaded', got %v", doneData["status"])
	}

	// Verify file_size was updated
	fileSize, ok := doneData["file_size"].(float64)
	if !ok || fileSize <= 0 {
		t.Errorf("Expected positive file_size, got %v", doneData["file_size"])
	}

	// Verify mime_type was updated
	if doneData["mime_type"] == nil || doneData["mime_type"] == "" {
		t.Error("Expected mime_type to be set")
	}

	// Step 4: Verify content was actually updated
	getArgs := map[string]interface{}{
		"content_id": contentID,
	}

	getArgsJSON, _ := json.Marshal(getArgs)
	getReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "get_content",
			Arguments: getArgsJSON,
		},
	}

	getResult, err := server.handleGetContent(ctx, getReq)
	if err != nil {
		t.Fatalf("handleGetContent failed: %v", err)
	}

	getTextContent := getResult.Content[0].(*mcp.TextContent)
	var getData map[string]interface{}
	json.Unmarshal([]byte(getTextContent.Text), &getData)

	if getData["status"] != "uploaded" {
		t.Errorf("Content status should be 'uploaded', got %v", getData["status"])
	}
}

func TestGetContentTool(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	// First upload a content
	ownerID := uuid.New()
	uploadArgs := map[string]interface{}{
		"owner_id": ownerID.String(),
		"name":     "test.txt",
		"data":     base64.StdEncoding.EncodeToString([]byte("test data")),
	}

	uploadArgsJSON, _ := json.Marshal(uploadArgs)
	uploadReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "upload_content",
			Arguments: uploadArgsJSON,
		},
	}

	uploadResult, err := server.handleUploadContent(ctx, uploadReq)
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	// Extract content ID
	textContent := uploadResult.Content[0].(*mcp.TextContent)
	var uploadData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &uploadData)
	contentID := uploadData["id"].(string)

	// Now get the content
	getArgs := map[string]interface{}{
		"content_id": contentID,
	}

	getArgsJSON, _ := json.Marshal(getArgs)
	getReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "get_content",
			Arguments: getArgsJSON,
		},
	}

	result, err := server.handleGetContent(ctx, getReq)
	if err != nil {
		t.Fatalf("handleGetContent failed: %v", err)
	}

	if result == nil {
		t.Fatal("Result is nil")
	}

	// Verify the result
	textContent = result.Content[0].(*mcp.TextContent)
	var resultData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &resultData)

	if resultData["id"] != contentID {
		t.Errorf("Expected ID %s, got %v", contentID, resultData["id"])
	}

	if resultData["name"] != "test.txt" {
		t.Errorf("Expected name 'test.txt', got %v", resultData["name"])
	}
}

func TestListContentTool(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Upload some test content
	for i := 0; i < 3; i++ {
		args := map[string]interface{}{
			"owner_id": ownerID.String(),
			"name":     "test" + string(rune('0'+i)) + ".txt",
			"data":     base64.StdEncoding.EncodeToString([]byte("test data")),
		}

		argsJSON, _ := json.Marshal(args)
		req := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "upload_content",
				Arguments: argsJSON,
			},
		}

		_, err := server.handleUploadContent(ctx, req)
		if err != nil {
			t.Fatalf("Upload failed: %v", err)
		}
	}

	// Now list content
	listArgs := map[string]interface{}{
		"owner_id": ownerID.String(),
		"limit":    10,
		"offset":   0,
	}

	listArgsJSON, _ := json.Marshal(listArgs)
	listReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "list_content",
			Arguments: listArgsJSON,
		},
	}

	result, err := server.handleListContent(ctx, listReq)
	if err != nil {
		t.Fatalf("handleListContent failed: %v", err)
	}

	// Verify the result
	textContent := result.Content[0].(*mcp.TextContent)
	var resultData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &resultData)

	items := resultData["items"].([]interface{})
	if len(items) != 3 {
		t.Errorf("Expected 3 items, got %d", len(items))
	}

	total := int(resultData["total"].(float64))
	if total != 3 {
		t.Errorf("Expected total 3, got %d", total)
	}
}

func TestListDerivedContentTool(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Upload a parent content (image)
	parentArgs := map[string]interface{}{
		"owner_id":  ownerID.String(),
		"name":      "parent-image.jpg",
		"data":      base64.StdEncoding.EncodeToString([]byte("parent image data")),
		"file_name": "parent-image.jpg",
	}

	parentArgsJSON, _ := json.Marshal(parentArgs)
	parentReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "upload_content",
			Arguments: parentArgsJSON,
		},
	}

	parentResult, err := server.handleUploadContent(ctx, parentReq)
	if err != nil {
		t.Fatalf("Parent upload failed: %v", err)
	}

	// Extract parent ID
	textContent := parentResult.Content[0].(*mcp.TextContent)
	var parentData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &parentData)
	parentID := parentData["id"].(string)

	// Upload some derived content (thumbnails)
	for i, size := range []string{"256", "512"} {
		derivedArgs := map[string]interface{}{
			"parent_id":       parentID,
			"name":            "thumbnail_" + size,
			"data":            base64.StdEncoding.EncodeToString([]byte("thumbnail " + size)),
			"derivation_type": "thumbnail",
			"variant":         "thumbnail_" + size,
		}

		derivedArgsJSON, _ := json.Marshal(derivedArgs)
		derivedReq := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "upload_content",
				Arguments: derivedArgsJSON,
			},
		}

		// Note: We're using upload_content, but need to create derived relationship
		// For proper testing, we'd need access to UploadDerivedContent or CreateDerivedContent
		// For now, we'll just test that list_derived_content doesn't error with no results
		_, _ = server.handleUploadContent(ctx, derivedReq)
		_ = i // avoid unused warning
	}

	// List derived content
	listArgs := map[string]interface{}{
		"parent_id": parentID,
	}

	listArgsJSON, _ := json.Marshal(listArgs)
	listReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "list_derived_content",
			Arguments: listArgsJSON,
		},
	}

	result, err := server.handleListDerivedContent(ctx, listReq)
	if err != nil {
		t.Fatalf("handleListDerivedContent failed: %v", err)
	}

	// Verify result structure
	textContent = result.Content[0].(*mcp.TextContent)
	var resultData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &resultData)

	if resultData["items"] == nil {
		t.Error("Result does not contain items")
	}
}

func TestGetContentStatusTool(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Upload content
	uploadArgs := map[string]interface{}{
		"owner_id": ownerID.String(),
		"name":     "test.txt",
		"data":     base64.StdEncoding.EncodeToString([]byte("test data")),
	}

	uploadArgsJSON, _ := json.Marshal(uploadArgs)
	uploadReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "upload_content",
			Arguments: uploadArgsJSON,
		},
	}

	uploadResult, err := server.handleUploadContent(ctx, uploadReq)
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	// Extract content ID
	textContent := uploadResult.Content[0].(*mcp.TextContent)
	var uploadData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &uploadData)
	contentID := uploadData["id"].(string)

	// Get content status
	statusArgs := map[string]interface{}{
		"content_id": contentID,
	}

	statusArgsJSON, _ := json.Marshal(statusArgs)
	statusReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "get_content_status",
			Arguments: statusArgsJSON,
		},
	}

	result, err := server.handleGetContentStatus(ctx, statusReq)
	if err != nil {
		t.Fatalf("handleGetContentStatus failed: %v", err)
	}

	// Verify result
	textContent = result.Content[0].(*mcp.TextContent)
	var resultData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &resultData)

	if resultData["id"] != contentID {
		t.Errorf("Expected ID %s, got %v", contentID, resultData["id"])
	}

	if resultData["status"] != "uploaded" {
		t.Errorf("Expected status 'uploaded', got %v", resultData["status"])
	}

	if resultData["ready"] != true {
		t.Errorf("Expected ready to be true, got %v", resultData["ready"])
	}
}

func TestListByStatusTool(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Upload some content
	for i := 0; i < 2; i++ {
		args := map[string]interface{}{
			"owner_id": ownerID.String(),
			"name":     "test" + string(rune('0'+i)) + ".txt",
			"data":     base64.StdEncoding.EncodeToString([]byte("test data")),
		}

		argsJSON, _ := json.Marshal(args)
		req := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "upload_content",
				Arguments: argsJSON,
			},
		}

		_, err := server.handleUploadContent(ctx, req)
		if err != nil {
			t.Fatalf("Upload failed: %v", err)
		}
	}

	// List by status
	listArgs := map[string]interface{}{
		"status": "uploaded",
	}

	listArgsJSON, _ := json.Marshal(listArgs)
	listReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "list_by_status",
			Arguments: listArgsJSON,
		},
	}

	result, err := server.handleListByStatus(ctx, listReq)
	if err != nil {
		t.Fatalf("handleListByStatus failed: %v", err)
	}

	// Verify result
	textContent := result.Content[0].(*mcp.TextContent)
	var resultData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &resultData)

	items := resultData["items"].([]interface{})
	if len(items) < 2 {
		t.Errorf("Expected at least 2 items, got %d", len(items))
	}

	if resultData["status"] != "uploaded" {
		t.Errorf("Expected status 'uploaded', got %v", resultData["status"])
	}
}

func TestResourceContent(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Upload content
	uploadArgs := map[string]interface{}{
		"owner_id": ownerID.String(),
		"name":     "test-resource.txt",
		"data":     base64.StdEncoding.EncodeToString([]byte("test data")),
	}

	uploadArgsJSON, _ := json.Marshal(uploadArgs)
	uploadReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "upload_content",
			Arguments: uploadArgsJSON,
		},
	}

	uploadResult, err := server.handleUploadContent(ctx, uploadReq)
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	textContent := uploadResult.Content[0].(*mcp.TextContent)
	var uploadData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &uploadData)
	contentID := uploadData["id"].(string)

	// Read resource: content://{id}
	resourceReq := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "content://" + contentID,
		},
	}

	result, err := server.handleContentResource(ctx, resourceReq)
	if err != nil {
		t.Fatalf("Read resource failed: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("No resource contents returned")
	}

	if result.Contents[0].MIMEType != "application/json" {
		t.Errorf("Expected MIME type application/json, got %s", result.Contents[0].MIMEType)
	}

	// Verify JSON content
	var resourceData map[string]interface{}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &resourceData); err != nil {
		t.Fatalf("Failed to parse resource JSON: %v", err)
	}

	if resourceData["id"] != contentID {
		t.Errorf("Expected ID %s, got %v", contentID, resourceData["id"])
	}
}

func TestResourceSchema(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	resourceReq := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "schema://content",
		},
	}

	result, err := server.handleContentSchemaResource(ctx, resourceReq)
	if err != nil {
		t.Fatalf("Read schema resource failed: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("No resource contents returned")
	}

	if result.Contents[0].MIMEType != "application/schema+json" {
		t.Errorf("Expected MIME type application/schema+json, got %s", result.Contents[0].MIMEType)
	}

	// Verify it's valid JSON Schema
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &schema); err != nil {
		t.Fatalf("Failed to parse schema JSON: %v", err)
	}

	if schema["$schema"] == nil {
		t.Error("Schema missing $schema field")
	}
}

func TestResourceStats(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	resourceReq := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "stats://system",
		},
	}

	result, err := server.handleSystemStatsResource(ctx, resourceReq)
	if err != nil {
		t.Fatalf("Read stats resource failed: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("No resource contents returned")
	}

	// Verify JSON content
	var stats map[string]interface{}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &stats); err != nil {
		t.Fatalf("Failed to parse stats JSON: %v", err)
	}

	if stats["content_count"] == nil {
		t.Error("Stats missing content_count")
	}
}

func TestPromptUploadWorkflow(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	promptReq := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name:      "upload-workflow",
			Arguments: map[string]string{},
		},
	}

	result, err := server.handleUploadWorkflowPrompt(ctx, promptReq)
	if err != nil {
		t.Fatalf("Get prompt failed: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("No prompt messages returned")
	}

	textContent, ok := result.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatal("Prompt message content is not TextContent")
	}

	if textContent.Text == "" {
		t.Error("Prompt message text is empty")
	}
}

func TestPromptSearchWorkflow(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	promptReq := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name: "search-workflow",
			Arguments: map[string]string{
				"search_type": "tags",
			},
		},
	}

	result, err := server.handleSearchWorkflowPrompt(ctx, promptReq)
	if err != nil {
		t.Fatalf("Get prompt failed: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("No prompt messages returned")
	}

	textContent, ok := result.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatal("Prompt message content is not TextContent")
	}

	if !strings.Contains(textContent.Text, "tags") {
		t.Error("Prompt should contain information about tags search")
	}
}

func TestBatchUpload(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Prepare batch items
	items := []map[string]interface{}{
		{
			"name":      "batch-item-1.txt",
			"data":      base64.StdEncoding.EncodeToString([]byte("batch data 1")),
			"file_name": "batch-item-1.txt",
			"tags":      []string{"batch", "test"},
		},
		{
			"name":      "batch-item-2.txt",
			"data":      base64.StdEncoding.EncodeToString([]byte("batch data 2")),
			"file_name": "batch-item-2.txt",
			"tags":      []string{"batch", "test"},
		},
		{
			"name":      "batch-item-3.txt",
			"data":      base64.StdEncoding.EncodeToString([]byte("batch data 3")),
			"file_name": "batch-item-3.txt",
			"tags":      []string{"batch", "test"},
		},
	}

	batchArgs := map[string]interface{}{
		"owner_id": ownerID.String(),
		"items":    items,
	}

	batchArgsJSON, _ := json.Marshal(batchArgs)
	batchReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "batch_upload",
			Arguments: batchArgsJSON,
		},
	}

	result, err := server.handleBatchUpload(ctx, batchReq)
	if err != nil {
		t.Fatalf("handleBatchUpload failed: %v", err)
	}

	// Verify result
	textContent := result.Content[0].(*mcp.TextContent)
	var resultData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &resultData)

	total := int(resultData["total"].(float64))
	if total != 3 {
		t.Errorf("Expected total 3, got %d", total)
	}

	successful := int(resultData["successful"].(float64))
	if successful != 3 {
		t.Errorf("Expected 3 successful uploads, got %d", successful)
	}

	failed := int(resultData["failed"].(float64))
	if failed != 0 {
		t.Errorf("Expected 0 failed uploads, got %d", failed)
	}

	// Verify results array
	results := resultData["results"].([]interface{})
	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	for _, r := range results {
		resultItem := r.(map[string]interface{})
		if !resultItem["success"].(bool) {
			t.Errorf("Upload should have succeeded, got error: %v", resultItem["error"])
		}
		if resultItem["content_id"] == nil || resultItem["content_id"].(string) == "" {
			t.Error("Expected content_id to be set")
		}
	}
}

func TestBatchGetDetails(t *testing.T) {
	server := createTestServer(t)
	ctx := context.Background()

	ownerID := uuid.New()

	// Upload some content first
	contentIDs := []string{}
	for i := 0; i < 3; i++ {
		uploadArgs := map[string]interface{}{
			"owner_id":  ownerID.String(),
			"name":      fmt.Sprintf("test%d.txt", i),
			"data":      base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("test data %d", i))),
			"file_name": fmt.Sprintf("test%d.txt", i),
		}

		uploadArgsJSON, _ := json.Marshal(uploadArgs)
		uploadReq := &mcp.CallToolRequest{
			Params: &mcp.CallToolParamsRaw{
				Name:      "upload_content",
				Arguments: uploadArgsJSON,
			},
		}

		uploadResult, err := server.handleUploadContent(ctx, uploadReq)
		if err != nil {
			t.Fatalf("Upload failed: %v", err)
		}

		textContent := uploadResult.Content[0].(*mcp.TextContent)
		var uploadData map[string]interface{}
		json.Unmarshal([]byte(textContent.Text), &uploadData)
		contentIDs = append(contentIDs, uploadData["id"].(string))
	}

	// Batch get details
	batchArgs := map[string]interface{}{
		"content_ids": contentIDs,
	}

	batchArgsJSON, _ := json.Marshal(batchArgs)
	batchReq := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "batch_get_details",
			Arguments: batchArgsJSON,
		},
	}

	result, err := server.handleBatchGetDetails(ctx, batchReq)
	if err != nil {
		t.Fatalf("handleBatchGetDetails failed: %v", err)
	}

	// Verify result
	textContent := result.Content[0].(*mcp.TextContent)
	var resultData map[string]interface{}
	json.Unmarshal([]byte(textContent.Text), &resultData)

	total := int(resultData["total"].(float64))
	if total != 3 {
		t.Errorf("Expected total 3, got %d", total)
	}

	successful := int(resultData["successful"].(float64))
	if successful != 3 {
		t.Errorf("Expected 3 successful fetches, got %d", successful)
	}

	// Verify results array
	results := resultData["results"].([]interface{})
	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	for _, r := range results {
		resultItem := r.(map[string]interface{})
		if resultItem["Error"] != nil && resultItem["Error"].(string) != "" {
			t.Errorf("Get details should have succeeded, got error: %v", resultItem["Error"])
		}
		if resultItem["Details"] == nil {
			t.Error("Expected Details to be set")
		}
	}
}
