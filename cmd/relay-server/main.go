package main

import (
	"context"
	"crypto/tls"
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
	defaultHTTPSPort  = "8443"
	defaultConfigPath = "auth-config.yaml"
)

// Server represents the relay server
type Server struct {
	httpServer  *http.Server
	httpsServer *http.Server
	authConfig  *middleware.AuthConfig
	aclConfig   *middleware.ACLConfig
	tlsEnabled  bool
}

func main() {
	// Parse command line flags
	port := flag.String("port", defaultPort, "Server HTTP port")
	httpsPort := flag.String("https-port", defaultHTTPSPort, "Server HTTPS port")
	configPath := flag.String("config", defaultConfigPath, "Path to auth configuration file")
	tlsConfigPath := flag.String("tls-config", "", "Path to TLS configuration file (optional)")
	flag.Parse()

	// Load authentication configuration
	log.Printf("Loading authentication configuration from: %s", *configPath)
	authConfig, err := config.LoadFromFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to load auth configuration: %v", err)
	}
	log.Println("Authentication configuration loaded successfully")

	// Load TLS configuration if provided
	var tlsConfig *tls.Config
	var tlsEnabled bool
	if *tlsConfigPath != "" {
		log.Printf("Loading TLS configuration from: %s", *tlsConfigPath)
		portalTLSConfig, err := config.LoadTLSConfig(*tlsConfigPath)
		if err != nil {
			log.Fatalf("Failed to load TLS configuration: %v", err)
		}
		tlsConfig = portalTLSConfig.GetTLSConfig()
		tlsEnabled = true
		log.Println("TLS configuration loaded successfully")

		// Display certificate info
		if certInfo, err := portalTLSConfig.GetCertificateInfo(); err == nil {
			log.Printf("Certificate Info:")
			log.Printf("  Subject: %s", certInfo.Subject)
			log.Printf("  Issuer: %s", certInfo.Issuer)
			log.Printf("  Valid: %s - %s", certInfo.NotBefore.Format("2006-01-02"), certInfo.NotAfter.Format("2006-01-02"))
			log.Printf("  Expires in: %v", certInfo.ExpiresIn())
		}
	}

	// Create server
	server := NewServer(*port, *httpsPort, authConfig, tlsConfig, tlsEnabled)

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// NewServer creates a new relay server instance
func NewServer(port, httpsPort string, authConfig *middleware.AuthConfig, tlsConfig *tls.Config, tlsEnabled bool) *Server {
	mux := http.NewServeMux()

	// Create ACL configuration
	aclConfig := middleware.NewACLConfig()

	// Create rate limit configuration
	// 100 req/s global, 50 req/s per API key, 10 req/s per IP
	rateLimitConfig := middleware.NewRateLimitConfig(100, 200)
	rateLimitConfig.PerKeyRequestsPerSecond = 50
	rateLimitConfig.PerKeyBurstSize = 100
	rateLimitConfig.PerIPRequestsPerSecond = 10
	rateLimitConfig.PerIPBurstSize = 20

	// Create middlewares
	authMiddleware := middleware.NewAuthMiddleware(authConfig)
	aclMiddleware := middleware.NewACLMiddleware(aclConfig)
	rateLimitMiddleware := middleware.NewRateLimitMiddleware(rateLimitConfig)

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

	// Apply auth and rate limit middleware to admin routes
	mux.Handle("/admin/", authMiddleware.Middleware(rateLimitMiddleware.Middleware(adminMux)))

	// Protected endpoints (authentication + ACL + rate limiting required)
	peerMux := http.NewServeMux()
	peerMux.HandleFunc("/peer/", handlePeerRequest)

	// Apply auth, rate limit, and ACL middleware to peer routes
	mux.Handle("/peer/", authMiddleware.Middleware(rateLimitMiddleware.Middleware(aclMiddleware.Middleware(peerMux))))

	// Auth validation endpoint (authentication + rate limiting only, no ACL)
	authValidateMux := http.NewServeMux()
	authValidateMux.HandleFunc("/auth/validate", handleAuthValidate)
	mux.Handle("/auth/validate", authMiddleware.Middleware(rateLimitMiddleware.Middleware(authValidateMux)))

	// Create HTTP server
	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Create HTTPS server if TLS is enabled
	var httpsServer *http.Server
	if tlsEnabled && tlsConfig != nil {
		httpsServer = &http.Server{
			Addr:         ":" + httpsPort,
			Handler:      mux,
			TLSConfig:    tlsConfig,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
	}

	return &Server{
		httpServer:  httpServer,
		httpsServer: httpsServer,
		authConfig:  authConfig,
		aclConfig:   aclConfig,
		tlsEnabled:  tlsEnabled,
	}
}

// Start starts the relay server
func (s *Server) Start() error {
	// Setup graceful shutdown
	shutdown := make(chan error, 1)
	go s.handleShutdown(shutdown)

	// Start HTTPS server if TLS is enabled
	if s.tlsEnabled && s.httpsServer != nil {
		go func() {
			log.Printf("Starting HTTPS server on %s", s.httpsServer.Addr)
			if err := s.httpsServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				shutdown <- fmt.Errorf("HTTPS server error: %w", err)
			}
		}()
	}

	// Start HTTP server
	log.Printf("Starting HTTP server on %s", s.httpServer.Addr)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdown <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

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

	// Shutdown HTTPS server if running
	if s.tlsEnabled && s.httpsServer != nil {
		log.Println("Shutting down HTTPS server...")
		if err := s.httpsServer.Shutdown(ctx); err != nil {
			shutdown <- fmt.Errorf("HTTPS server shutdown error: %w", err)
			return
		}
	}

	// Shutdown HTTP server
	log.Println("Shutting down HTTP server...")
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
