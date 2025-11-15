package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/portal-project/portal-gateway/portal/config"
	"github.com/portal-project/portal-gateway/portal/middleware"
)

const (
	defaultPort       = "8080"
	defaultConfigPath = "auth-config.yaml"
)

// Server represents the relay server
type Server struct {
	httpServer *http.Server
	authConfig *middleware.AuthConfig
	aclConfig  *middleware.ACLConfig
}

func main() {
	// Parse command line flags
	port := flag.String("port", defaultPort, "Server port")
	configPath := flag.String("config", defaultConfigPath, "Path to auth configuration file")
	flag.Parse()

	// Load authentication configuration
	log.Printf("Loading authentication configuration from: %s", *configPath)
	authConfig, err := config.LoadFromFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to load auth configuration: %v", err)
	}
	log.Println("Authentication configuration loaded successfully")

	// Create server
	server := NewServer(*port, authConfig)

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// NewServer creates a new relay server instance
func NewServer(port string, authConfig *middleware.AuthConfig) *Server {
	mux := http.NewServeMux()

	// Create ACL configuration
	aclConfig := middleware.NewACLConfig()

	// Create middlewares
	authMiddleware := middleware.NewAuthMiddleware(authConfig)
	aclMiddleware := middleware.NewACLMiddleware(aclConfig)

	// Create admin handler
	adminHandler := NewAdminHandler(aclConfig)

	// Public endpoints (no authentication required)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/", handleRoot)

	// Admin endpoints (authentication + admin scope required)
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/admin/acl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/admin/acl" {
			adminHandler.HandleListACLRules(w, r)
		} else if r.Method == http.MethodPost {
			adminHandler.HandleAddACLRule(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	adminMux.HandleFunc("/admin/acl/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			adminHandler.HandleGetACLRule(w, r)
		} else if r.Method == http.MethodDelete {
			adminHandler.HandleRemoveACLRule(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Apply auth middleware to admin routes
	mux.Handle("/admin/", authMiddleware.Middleware(adminMux))

	// Protected endpoints (authentication + ACL required)
	peerMux := http.NewServeMux()
	peerMux.HandleFunc("/peer/", handlePeerRequest)

	// Apply auth and ACL middleware to peer routes
	mux.Handle("/peer/", authMiddleware.Middleware(aclMiddleware.Middleware(peerMux)))

	// Auth validation endpoint (authentication only, no ACL)
	authValidateMux := http.NewServeMux()
	authValidateMux.HandleFunc("/auth/validate", handleAuthValidate)
	mux.Handle("/auth/validate", authMiddleware.Middleware(authValidateMux))

	// Create HTTP server
	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		httpServer: httpServer,
		authConfig: authConfig,
		aclConfig:  aclConfig,
	}
}

// Start starts the relay server
func (s *Server) Start() error {
	// Setup graceful shutdown
	shutdown := make(chan error, 1)
	go s.handleShutdown(shutdown)

	log.Printf("Starting relay server on %s", s.httpServer.Addr)

	// Start HTTP server
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	// Wait for shutdown signal
	return <-shutdown
}

// handleShutdown handles graceful shutdown on SIGTERM/SIGINT
func (s *Server) handleShutdown(shutdown chan<- error) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	log.Printf("Received signal: %v, initiating graceful shutdown...", sig)

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown HTTP server
	if err := s.httpServer.Shutdown(ctx); err != nil {
		shutdown <- fmt.Errorf("HTTP server shutdown error: %w", err)
		return
	}

	log.Println("Server shutdown completed successfully")
	shutdown <- nil
}

// handleHealth handles health check requests
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"healthy","timestamp":"%s"}`, time.Now().Format(time.RFC3339))
}

// handleRoot handles root path requests
func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"service":"portal-gateway","version":"0.1.0","status":"running"}`)
}

// handlePeerRequest handles peer relay requests (requires authentication + ACL)
func handlePeerRequest(w http.ResponseWriter, r *http.Request) {
	// Get API key info from context
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}

	// Get lease ID from context (set by ACL middleware)
	leaseID := middleware.GetLeaseID(r.Context())
	if leaseID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"invalid_lease_id","message":"Lease ID is required"}`)
		return
	}

	// Check if API key has required scope
	if !apiKeyInfo.HasScope("write") && !apiKeyInfo.HasScope("admin") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, `{"error":"insufficient_permissions","message":"This endpoint requires 'write' or 'admin' scope"}`)
		return
	}

	// TODO: Implement actual peer relay logic
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"message":"Peer relay endpoint (implementation pending)","authenticated_as":"%s","lease_id":"%s"}`, apiKeyInfo.KeyID, leaseID)
}

// handleAuthValidate handles API key validation requests (requires authentication)
func handleAuthValidate(w http.ResponseWriter, r *http.Request) {
	// Get API key info from context
	apiKeyInfo := middleware.GetAPIKeyInfo(r.Context())
	if apiKeyInfo == nil {
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	}

	// Return API key information
	expiresAt := "never"
	if apiKeyInfo.ExpiresAt != nil {
		expiresAt = apiKeyInfo.ExpiresAt.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"valid":true,"key_id":"%s","scopes":%q,"expires_at":"%s"}`,
		apiKeyInfo.KeyID,
		apiKeyInfo.Scopes,
		expiresAt,
	)
}
