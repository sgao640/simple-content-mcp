package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tendant/simple-content/pkg/simplecontent"
	"github.com/tendant/simple-content/pkg/simplecontent/admin"

	mcperrors "github.com/tendant/simple-content-mcp/pkg/mcpserver/errors"
)

// handleUploadContent uploads content with data in a single operation
func (s *Server) handleUploadContent(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Unmarshal arguments
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	// Parse and validate required fields
	ownerID, err := parseUUID(params["owner_id"])
	if err != nil {
		return nil, mcperrors.NewValidationError("owner_id", err)
	}

	name, ok := params["name"].(string)
	if !ok || name == "" {
		return nil, mcperrors.NewValidationError("name", fmt.Errorf("required"))
	}

	// Decode data (base64 or URL)
	reader, err := s.decodeData(params["data"])
	if err != nil {
		return nil, err
	}

	// Build upload request
	uploadReq := simplecontent.UploadContentRequest{
		OwnerID:            ownerID,
		TenantID:           parseTenantID(params["tenant_id"]),
		Name:               name,
		Description:        getStringOr(params, "description", ""),
		DocumentType:       getStringOr(params, "document_type", "application/octet-stream"),
		StorageBackendName: getStringOr(params, "storage_backend", ""),
		Reader:             reader,
		FileName:           getStringOr(params, "file_name", ""),
		Tags:               getStringSlice(params, "tags"),
		CustomMetadata:     getMap(params, "metadata"),
	}

	// Call service
	content, err := s.service.UploadContent(ctx, uploadReq)
	if err != nil {
		return nil, s.mapError(err)
	}

	// Get download URL using GetContentDetails
	details, err := s.service.GetContentDetails(ctx, content.ID)
	if err != nil {
		// Don't fail if we can't get details, just return without URL
		return newTextResult(formatJSON(map[string]interface{}{
			"id":         content.ID.String(),
			"status":     string(content.Status),
			"created_at": content.CreatedAt,
		})), nil
	}

	return newTextResult(formatJSON(map[string]interface{}{
		"id":           content.ID.String(),
		"status":       string(content.Status),
		"download_url": details.Download,
		"created_at":   content.CreatedAt,
	})), nil
}

// handleCreateUpload creates content metadata and returns an upload URL for async upload
func (s *Server) handleCreateUpload(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Unmarshal arguments
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	// Parse and validate required fields
	ownerID, err := parseUUID(params["owner_id"])
	if err != nil {
		return nil, mcperrors.NewValidationError("owner_id", err)
	}

	name, ok := params["name"].(string)
	if !ok || name == "" {
		return nil, mcperrors.NewValidationError("name", fmt.Errorf("required"))
	}

	// Build create content request
	createReq := simplecontent.CreateContentRequest{
		OwnerID:        ownerID,
		TenantID:       parseTenantID(params["tenant_id"]),
		Name:           name,
		Description:    getStringOr(params, "description", ""),
		DocumentType:   getStringOr(params, "document_type", "application/octet-stream"),
		DerivationType: simplecontent.ContentDerivationTypeOriginal,
	}

	// Call service to create content
	content, err := s.service.CreateContent(ctx, createReq)
	if err != nil {
		return nil, s.mapError(err)
	}

	// Set metadata if provided
	tags := getStringSlice(params, "tags")
	customMetadata := getMap(params, "metadata")
	fileName := getStringOr(params, "file_name", "")

	if len(tags) > 0 || customMetadata != nil || fileName != "" {
		metadataReq := simplecontent.SetContentMetadataRequest{
			ContentID:      content.ID,
			Tags:           tags,
			FileName:       fileName,
			CustomMetadata: customMetadata,
		}
		if err := s.service.SetContentMetadata(ctx, metadataReq); err != nil {
			// Don't fail if metadata setting fails, just log it
			// The content is already created
		}
	}

	// Create object placeholder for the content
	storageBackend := getStringOr(params, "storage_backend", "default")
	createObjReq := simplecontent.CreateObjectRequest{
		ContentID:          content.ID,
		StorageBackendName: storageBackend,
		Version:            1,
		ObjectKey:          fmt.Sprintf("%s/%s", content.ID.String(), fileName),
	}

	obj, err := s.storageService.CreateObject(ctx, createObjReq)
	if err != nil {
		return nil, s.mapError(err)
	}

	// Get upload URL for the object
	uploadURL, err := s.storageService.GetUploadURL(ctx, obj.ID)
	if err != nil {
		return nil, s.mapError(err)
	}

	// Return content_id, upload_url, and status
	return newTextResult(formatJSON(map[string]interface{}{
		"content_id": content.ID.String(),
		"upload_url": uploadURL,
		"status":     string(content.Status),
		"created_at": content.CreatedAt,
	})), nil
}

// handleGetContent retrieves content metadata by ID
func (s *Server) handleGetContent(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	contentID, err := parseUUID(params["content_id"])
	if err != nil {
		return nil, mcperrors.NewValidationError("content_id", err)
	}

	content, err := s.service.GetContent(ctx, contentID)
	if err != nil {
		return nil, s.mapError(err)
	}

	return newTextResult(formatJSON(map[string]interface{}{
		"id":              content.ID.String(),
		"owner_id":        content.OwnerID.String(),
		"tenant_id":       content.TenantID.String(),
		"name":            content.Name,
		"description":     content.Description,
		"status":          string(content.Status),
		"derivation_type": content.DerivationType,
		"created_at":      content.CreatedAt,
		"updated_at":      content.UpdatedAt,
	})), nil
}

// handleGetContentDetails gets complete content information
func (s *Server) handleGetContentDetails(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	contentID, err := parseUUID(params["content_id"])
	if err != nil {
		return nil, mcperrors.NewValidationError("content_id", err)
	}

	includeUpload := getBoolOr(params, "include_upload_url", false)

	var options []simplecontent.ContentDetailsOption
	if includeUpload {
		options = append(options, simplecontent.WithUploadAccess())
	}

	details, err := s.service.GetContentDetails(ctx, contentID, options...)
	if err != nil {
		return nil, s.mapError(err)
	}

	return newTextResult(formatJSON(map[string]interface{}{
		"id":         details.ID,
		"download":   details.Download,
		"upload":     details.Upload,
		"preview":    details.Preview,
		"thumbnail":  details.Thumbnail,
		"thumbnails": details.Thumbnails,
		"previews":   details.Previews,
		"transcodes": details.Transcodes,
		"file_name":  details.FileName,
		"file_size":  details.FileSize,
		"mime_type":  details.MimeType,
		"tags":       details.Tags,
		"checksum":   details.Checksum,
		"ready":      details.Ready,
		"expires_at": details.ExpiresAt,
		"created_at": details.CreatedAt,
		"updated_at": details.UpdatedAt,
	})), nil
}

// handleListContent lists content with filtering and pagination
func (s *Server) handleListContent(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	var contents []*simplecontent.Content
	var err error

	// Get pagination parameters
	limit := getIntOr(params, "limit", s.config.DefaultPageSize)
	offset := getIntOr(params, "offset", 0)

	// Use admin service if RequireOwnerID is false and admin service is available
	if !s.config.RequireOwnerID && s.adminService != nil {
		// Build filters for admin operations
		filters := admin.ContentFilters{
			Limit:  &limit,
			Offset: &offset,
		}

		if ownerID, err := parseUUID(params["owner_id"]); err == nil && ownerID != uuid.Nil {
			filters.OwnerID = &ownerID
		}

		if tenantID, err := parseUUID(params["tenant_id"]); err == nil && tenantID != uuid.Nil {
			filters.TenantID = &tenantID
		}

		if statusStr := getStringOr(params, "status", ""); statusStr != "" {
			filters.Status = &statusStr
		}

		// Call admin list
		req := admin.ListContentsRequest{
			Filters: filters,
		}
		resp, err := s.adminService.ListAllContents(ctx, req)
		if err != nil {
			return nil, s.mapError(err)
		}
		contents = resp.Contents
	} else {
		// Use standard service method (requires owner_id)
		listReq := simplecontent.ListContentRequest{}

		if ownerID, err := parseUUID(params["owner_id"]); err == nil {
			listReq.OwnerID = ownerID
		}

		if tenantID, err := parseUUID(params["tenant_id"]); err == nil {
			listReq.TenantID = tenantID
		}

		// Call service
		contents, err = s.service.ListContent(ctx, listReq)
		if err != nil {
			return nil, s.mapError(err)
		}

		// Apply client-side filtering for status since ListContent doesn't support it
		if statusStr := getStringOr(params, "status", ""); statusStr != "" {
			temp := make([]*simplecontent.Content, 0)
			for _, c := range contents {
				if string(c.Status) == statusStr {
					temp = append(temp, c)
				}
			}
			contents = temp
		}
	}

	// Apply client-side pagination only for standard service method
	// (admin service already handled pagination)
	pagedContents := contents
	if s.config.RequireOwnerID || s.adminService == nil {
		start := offset
		end := offset + limit
		if start > len(contents) {
			start = len(contents)
		}
		if end > len(contents) {
			end = len(contents)
		}
		pagedContents = contents[start:end]
	}

	// Format results
	items := make([]map[string]interface{}, len(pagedContents))
	for i, content := range pagedContents {
		items[i] = map[string]interface{}{
			"id":              content.ID.String(),
			"owner_id":        content.OwnerID.String(),
			"tenant_id":       content.TenantID.String(),
			"name":            content.Name,
			"description":     content.Description,
			"status":          string(content.Status),
			"derivation_type": content.DerivationType,
			"created_at":      content.CreatedAt,
			"updated_at":      content.UpdatedAt,
		}
	}

	return newTextResult(formatJSON(map[string]interface{}{
		"items":  items,
		"total":  len(contents),
		"limit":  limit,
		"offset": offset,
	})), nil
}

// handleDownloadContent downloads content data
func (s *Server) handleDownloadContent(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	contentID, err := parseUUID(params["content_id"])
	if err != nil {
		return nil, mcperrors.NewValidationError("content_id", err)
	}

	format := getStringOr(params, "format", "url")

	// Get content details for URL
	details, err := s.service.GetContentDetails(ctx, contentID)
	if err != nil {
		return nil, s.mapError(err)
	}

	if format == "url" {
		return newTextResult(formatJSON(map[string]interface{}{
			"download_url": details.Download,
			"file_name":    details.FileName,
			"mime_type":    details.MimeType,
			"size":         details.FileSize,
		})), nil
	}

	// Download as base64
	reader, err := s.service.DownloadContent(ctx, contentID)
	if err != nil {
		return nil, s.mapError(err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, mcperrors.NewInternalError(fmt.Errorf("failed to read content: %w", err))
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	return newTextResult(formatJSON(map[string]interface{}{
		"data":      encoded,
		"file_name": details.FileName,
		"mime_type": details.MimeType,
		"size":      len(data),
	})), nil
}

// handleUpdateContent updates content metadata
func (s *Server) handleUpdateContent(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	contentID, err := parseUUID(params["content_id"])
	if err != nil {
		return nil, mcperrors.NewValidationError("content_id", err)
	}

	// Get current content
	content, err := s.service.GetContent(ctx, contentID)
	if err != nil {
		return nil, s.mapError(err)
	}

	// Update fields if provided
	if name := getStringOr(params, "name", ""); name != "" {
		content.Name = name
	}

	if desc := getStringOr(params, "description", ""); desc != "" {
		content.Description = desc
	}

	// Build update request
	updateReq := simplecontent.UpdateContentRequest{
		Content: content,
	}

	err = s.service.UpdateContent(ctx, updateReq)
	if err != nil {
		return nil, s.mapError(err)
	}

	// If tags or metadata provided, update them separately
	if tags := getStringSlice(params, "tags"); len(tags) > 0 || getMap(params, "metadata") != nil {
		metadataReq := simplecontent.SetContentMetadataRequest{
			ContentID:      contentID,
			Tags:           tags,
			CustomMetadata: getMap(params, "metadata"),
		}
		if err := s.service.SetContentMetadata(ctx, metadataReq); err != nil {
			return nil, s.mapError(err)
		}
	}

	return newTextResult(formatJSON(map[string]interface{}{
		"success":    true,
		"updated_at": time.Now(),
	})), nil
}

// handleDeleteContent soft deletes content
func (s *Server) handleDeleteContent(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	contentID, err := parseUUID(params["content_id"])
	if err != nil {
		return nil, mcperrors.NewValidationError("content_id", err)
	}

	err = s.service.DeleteContent(ctx, contentID)
	if err != nil {
		return nil, s.mapError(err)
	}

	return newTextResult(formatJSON(map[string]interface{}{
		"success":    true,
		"deleted_at": time.Now(),
	})), nil
}

// handleSearchContent searches content by metadata, tags, or full-text
func (s *Server) handleSearchContent(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
		return nil, mcperrors.NewValidationError("arguments", err)
	}

	// Build request - use basic ListContent
	// Note: simple-content doesn't have a dedicated search method
	// We'll use ListContent and filter client-side for Phase 1
	listReq := simplecontent.ListContentRequest{}

	if ownerID, err := parseUUID(params["owner_id"]); err == nil {
		listReq.OwnerID = ownerID
	}

	if tenantID, err := parseUUID(params["tenant_id"]); err == nil {
		listReq.TenantID = tenantID
	}

	// Call service
	contents, err := s.service.ListContent(ctx, listReq)
	if err != nil {
		return nil, s.mapError(err)
	}

	// Get pagination parameters
	limit := getIntOr(params, "limit", s.config.DefaultPageSize)
	offset := getIntOr(params, "offset", 0)

	// Apply client-side filtering
	filtered := contents

	// Query-based filtering (search in name and description)
	query := getStringOr(params, "query", "")
	if query != "" {
		temp := make([]*simplecontent.Content, 0)
		queryLower := toLowerString(query)
		for _, content := range filtered {
			if containsString(toLowerString(content.Name), queryLower) ||
				containsString(toLowerString(content.Description), queryLower) {
				temp = append(temp, content)
			}
		}
		filtered = temp
	}

	// Status filtering (support array of statuses)
	if statusArray := params["status"]; statusArray != nil {
		if statusSlice, ok := statusArray.([]interface{}); ok && len(statusSlice) > 0 {
			temp := make([]*simplecontent.Content, 0)
			statusMap := make(map[string]bool)
			for _, s := range statusSlice {
				if statusStr, ok := s.(string); ok {
					statusMap[statusStr] = true
				}
			}
			for _, content := range filtered {
				if statusMap[string(content.Status)] {
					temp = append(temp, content)
				}
			}
			filtered = temp
		}
	}

	// Apply pagination
	start := offset
	end := offset + limit
	if start > len(filtered) {
		start = len(filtered)
	}
	if end > len(filtered) {
		end = len(filtered)
	}
	pagedContents := filtered[start:end]

	// Format results
	items := make([]map[string]interface{}, len(pagedContents))
	for i, content := range pagedContents {
		items[i] = map[string]interface{}{
			"id":              content.ID.String(),
			"owner_id":        content.OwnerID.String(),
			"tenant_id":       content.TenantID.String(),
			"name":            content.Name,
			"description":     content.Description,
			"status":          string(content.Status),
			"derivation_type": content.DerivationType,
			"created_at":      content.CreatedAt,
			"updated_at":      content.UpdatedAt,
		}
	}

	return newTextResult(formatJSON(map[string]interface{}{
		"items":  items,
		"total":  len(filtered),
		"limit":  limit,
		"offset": offset,
	})), nil
}

// Helper functions for string operations

func toLowerString(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + ('a' - 'A')
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func containsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if matchAtPos(s, substr, i) {
			return true
		}
	}
	return false
}

func matchAtPos(s, substr string, pos int) bool {
	for i := 0; i < len(substr); i++ {
		if s[pos+i] != substr[i] {
			return false
		}
	}
	return true
}
