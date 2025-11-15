package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/portal-project/portal-gateway/portal/middleware"
	"github.com/portal-project/portal-gateway/portal/quota"
)

// AdminHandler handles administrative operations
type AdminHandler struct {
	aclConfig    *middleware.ACLConfig
	quotaManager *quota.Manager
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(aclConfig *middleware.ACLConfig, quotaManager *quota.Manager) *AdminHandler {
	return &AdminHandler{
		aclConfig:    aclConfig,
		quotaManager: quotaManager,
	}
}

// ACLRuleRequest represents a request to add/update an ACL rule
type ACLRuleRequest struct {
	LeaseID         string   `json:"lease_id"`
	AllowedKeyIDs   []string `json:"allowed_key_ids"`
	AllowedIPRanges []string `json:"allowed_ip_ranges,omitempty"` // CIDR notation
}

// ACLRuleResponse represents an ACL rule in responses
type ACLRuleResponse struct {
	LeaseID         string   `json:"lease_id"`
	AllowedKeyIDs   []string `json:"allowed_key_ids"`
	AllowedIPRanges []string `json:"allowed_ip_ranges,omitempty"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// SuccessResponse represents a success response
type SuccessResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// HandleAddACLRule handles POST /admin/acl
func (h *AdminHandler) HandleAddACLRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is allowed")
		return
	}

	// Check if requester has admin scope
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil || !apiKeyInfo.HasScope("admin") {
		h.sendError(w, http.StatusForbidden, "insufficient_permissions", "Admin scope required")
		return
	}

	// Parse request body
	var req ACLRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}

	// Validate request
	if err := h.validateACLRuleRequest(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}

	// Parse IP ranges if provided
	var ipNets []*net.IPNet
	if len(req.AllowedIPRanges) > 0 {
		var err error
		ipNets, err = middleware.ParseCIDRList(req.AllowedIPRanges)
		if err != nil {
			h.sendError(w, http.StatusBadRequest, "invalid_ip_range", err.Error())
			return
		}
	}

	// Create ACL rule
	rule := &middleware.ACLRule{
		LeaseID:         req.LeaseID,
		AllowedKeyIDs:   req.AllowedKeyIDs,
		AllowedIPRanges: ipNets,
	}

	// Add rule to configuration
	if err := h.aclConfig.AddRule(rule); err != nil {
		h.sendError(w, http.StatusInternalServerError, "add_rule_failed", err.Error())
		return
	}

	h.sendSuccess(w, http.StatusCreated, fmt.Sprintf("ACL rule for lease %s created successfully", req.LeaseID))
}

// HandleRemoveACLRule handles DELETE /admin/acl/{leaseID}
func (h *AdminHandler) HandleRemoveACLRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		h.sendError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only DELETE is allowed")
		return
	}

	// Check if requester has admin scope
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil || !apiKeyInfo.HasScope("admin") {
		h.sendError(w, http.StatusForbidden, "insufficient_permissions", "Admin scope required")
		return
	}

	// Extract lease ID from URL
	leaseID := extractLeaseIDFromPath(r.URL.Path, "/admin/acl/")
	if leaseID == "" {
		h.sendError(w, http.StatusBadRequest, "invalid_lease_id", "Lease ID is required")
		return
	}

	// Remove rule
	if err := h.aclConfig.RemoveRule(leaseID); err != nil {
		h.sendError(w, http.StatusNotFound, "rule_not_found", err.Error())
		return
	}

	h.sendSuccess(w, http.StatusOK, fmt.Sprintf("ACL rule for lease %s removed successfully", leaseID))
}

// HandleGetACLRule handles GET /admin/acl/{leaseID}
func (h *AdminHandler) HandleGetACLRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET is allowed")
		return
	}

	// Check if requester has admin scope
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil || !apiKeyInfo.HasScope("admin") {
		h.sendError(w, http.StatusForbidden, "insufficient_permissions", "Admin scope required")
		return
	}

	// Extract lease ID from URL
	leaseID := extractLeaseIDFromPath(r.URL.Path, "/admin/acl/")
	if leaseID == "" {
		h.sendError(w, http.StatusBadRequest, "invalid_lease_id", "Lease ID is required")
		return
	}

	// Get rule
	rule := h.aclConfig.GetRule(leaseID)
	if rule == nil {
		h.sendError(w, http.StatusNotFound, "rule_not_found", fmt.Sprintf("No ACL rule found for lease %s", leaseID))
		return
	}

	// Convert to response format
	response := h.ruleToResponse(rule)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.sendError(w, http.StatusInternalServerError, "encoding_failed", "Failed to encode response")
		return
	}
}

// HandleListACLRules handles GET /admin/acl
func (h *AdminHandler) HandleListACLRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET is allowed")
		return
	}

	// Check if requester has admin scope
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil || !apiKeyInfo.HasScope("admin") {
		h.sendError(w, http.StatusForbidden, "insufficient_permissions", "Admin scope required")
		return
	}

	// Get all rules
	rules := h.aclConfig.ListRules()

	// Convert to response format
	responses := make([]ACLRuleResponse, 0, len(rules))
	for _, rule := range rules {
		responses = append(responses, h.ruleToResponse(rule))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(responses); err != nil {
		h.sendError(w, http.StatusInternalServerError, "encoding_failed", "Failed to encode response")
		return
	}
}

// validateACLRuleRequest validates an ACL rule request
func (h *AdminHandler) validateACLRuleRequest(req *ACLRuleRequest) error {
	if req.LeaseID == "" {
		return errors.New("lease_id is required")
	}

	if len(req.AllowedKeyIDs) == 0 {
		return errors.New("at least one allowed_key_id is required")
	}

	return nil
}

// ruleToResponse converts an ACL rule to a response format
func (h *AdminHandler) ruleToResponse(rule *middleware.ACLRule) ACLRuleResponse {
	response := ACLRuleResponse{
		LeaseID:       rule.LeaseID,
		AllowedKeyIDs: rule.AllowedKeyIDs,
	}

	// Convert IPNets to CIDR strings
	if len(rule.AllowedIPRanges) > 0 {
		cidrs := make([]string, 0, len(rule.AllowedIPRanges))
		for _, ipNet := range rule.AllowedIPRanges {
			cidrs = append(cidrs, ipNet.String())
		}
		response.AllowedIPRanges = cidrs
	}

	return response
}

// extractLeaseIDFromPath extracts the lease ID from a URL path
func extractLeaseIDFromPath(urlPath, prefix string) string {
	if !strings.HasPrefix(urlPath, prefix) {
		return ""
	}

	leaseID := strings.TrimPrefix(urlPath, prefix)
	leaseID = strings.TrimSuffix(leaseID, "/")

	return leaseID
}

// sendError sends an error response
func (h *AdminHandler) sendError(w http.ResponseWriter, statusCode int, errorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := ErrorResponse{
		Error:   errorCode,
		Message: message,
	}

	json.NewEncoder(w).Encode(response)
}

// sendSuccess sends a success response
func (h *AdminHandler) sendSuccess(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	response := SuccessResponse{
		Success: true,
		Message: message,
	}

	json.NewEncoder(w).Encode(response)
}

// QuotaLimitRequest represents a request to set quota limits
type QuotaLimitRequest struct {
	KeyID                 string `json:"key_id"`
	MonthlyRequestLimit   int64  `json:"monthly_request_limit"`
	MonthlyBytesLimit     int64  `json:"monthly_bytes_limit"`
	ConcurrentConnections int    `json:"concurrent_connections"`
}

// HandleGetQuotaStatus handles GET /admin/quota/{keyID}
func (h *AdminHandler) HandleGetQuotaStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.sendError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only GET is allowed")
		return
	}

	// Check if requester has admin scope
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil || !apiKeyInfo.HasScope("admin") {
		h.sendError(w, http.StatusForbidden, "insufficient_permissions", "Admin scope required")
		return
	}

	// Extract key ID from URL
	keyID := extractLeaseIDFromPath(r.URL.Path, "/admin/quota/")
	if keyID == "" {
		h.sendError(w, http.StatusBadRequest, "invalid_key_id", "Key ID is required")
		return
	}

	// Get quota status
	status, err := h.quotaManager.GetStatus(keyID)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "get_status_failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// HandleSetQuotaLimit handles POST /admin/quota/{keyID}
func (h *AdminHandler) HandleSetQuotaLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is allowed")
		return
	}

	// Check if requester has admin scope
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil || !apiKeyInfo.HasScope("admin") {
		h.sendError(w, http.StatusForbidden, "insufficient_permissions", "Admin scope required")
		return
	}

	// Parse request body
	var req QuotaLimitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}

	// Validate key ID
	if req.KeyID == "" {
		h.sendError(w, http.StatusBadRequest, "invalid_key_id", "Key ID is required")
		return
	}

	// Create quota limit
	limit := &quota.QuotaLimit{
		KeyID:                 req.KeyID,
		MonthlyRequestLimit:   req.MonthlyRequestLimit,
		MonthlyBytesLimit:     req.MonthlyBytesLimit,
		ConcurrentConnections: req.ConcurrentConnections,
	}

	// Set limit
	if err := h.quotaManager.SetLimit(limit); err != nil {
		h.sendError(w, http.StatusBadRequest, "set_limit_failed", err.Error())
		return
	}

	h.sendSuccess(w, http.StatusOK, fmt.Sprintf("Quota limit for key %s updated successfully", req.KeyID))
}

// HandleResetQuota handles POST /admin/quota/{keyID}/reset
func (h *AdminHandler) HandleResetQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.sendError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is allowed")
		return
	}

	// Check if requester has admin scope
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil || !apiKeyInfo.HasScope("admin") {
		h.sendError(w, http.StatusForbidden, "insufficient_permissions", "Admin scope required")
		return
	}

	// Extract key ID from URL (remove "/reset" suffix)
	path := strings.TrimSuffix(r.URL.Path, "/reset")
	keyID := extractLeaseIDFromPath(path, "/admin/quota/")
	if keyID == "" {
		h.sendError(w, http.StatusBadRequest, "invalid_key_id", "Key ID is required")
		return
	}

	// Reset quota
	if err := h.quotaManager.ResetQuota(keyID); err != nil {
		h.sendError(w, http.StatusInternalServerError, "reset_failed", err.Error())
		return
	}

	h.sendSuccess(w, http.StatusOK, fmt.Sprintf("Quota for key %s reset successfully", keyID))
}
