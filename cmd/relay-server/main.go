package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/portal-project/portal-gateway/portal/circuitbreaker"
	"github.com/portal-project/portal-gateway/portal/config"
	"github.com/portal-project/portal-gateway/portal/logging"
	"github.com/portal-project/portal-gateway/portal/metrics"
	"github.com/portal-project/portal-gateway/portal/middleware"
	"github.com/portal-project/portal-gateway/portal/quota"
	"github.com/portal-project/portal-gateway/portal/shutdown"
	"github.com/portal-project/portal-gateway/portal/streaming"
	"github.com/portal-project/portal-gateway/portal/timeout"
)

const (
	defaultPort       = "8080"
	defaultHTTPSPort  = "8443"
	defaultConfigPath = "auth-config.yaml"
)

// Server represents the relay server
type Server struct {
	httpServer      *http.Server
	httpsServer     *http.Server
	authConfig      *middleware.AuthConfig
	aclConfig       *middleware.ACLConfig
	tlsEnabled      bool
	shutdownManager *shutdown.Manager
}

func main() {
	// Initialize structured logger
	logFormat := logging.FormatJSON
	if os.Getenv("LOG_FORMAT") == "text" {
		logFormat = logging.FormatText
	}

	logLevel := slog.LevelInfo
	if levelStr := os.Getenv("LOG_LEVEL"); levelStr != "" {
		switch strings.ToUpper(levelStr) {
		case "DEBUG":
			logLevel = slog.LevelDebug
		case "INFO":
			logLevel = slog.LevelInfo
		case "WARN":
			logLevel = slog.LevelWarn
		case "ERROR":
			logLevel = slog.LevelError
		}
	}

	logger := logging.NewLogger(&logging.Config{
		Level:  logLevel,
		Format: logFormat,
		Output: os.Stdout,
	})
	logging.SetDefault(logger)

	// Parse command line flags
	port := flag.String("port", defaultPort, "Server HTTP port")
	httpsPort := flag.String("https-port", defaultHTTPSPort, "Server HTTPS port")
	configPath := flag.String("config", defaultConfigPath, "Path to auth configuration file")
	tlsConfigPath := flag.String("tls-config", "", "Path to TLS configuration file (optional)")
	leaseRateLimitConfigPath := flag.String("lease-rate-limit-config", "", "Path to lease rate limit configuration file (optional)")
	quotaConfigPath := flag.String("quota-config", "", "Path to quota configuration file (optional)")
	flag.Parse()

	// Load authentication configuration
	logging.Info("Loading authentication configuration", "path", *configPath)
	authConfig, err := config.LoadFromFile(*configPath)
	if err != nil {
		logging.Error("Failed to load auth configuration", "error", err)
		os.Exit(1)
	}
	logging.Info("Authentication configuration loaded successfully")

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

	// Load lease rate limit configuration if provided
	var leaseRateLimitConfig *middleware.LeaseRateLimitConfig
	if *leaseRateLimitConfigPath != "" {
		log.Printf("Loading lease rate limit configuration from: %s", *leaseRateLimitConfigPath)
		leaseRateLimitConfig, err = config.LoadLeaseRateLimitConfig(*leaseRateLimitConfigPath)
		if err != nil {
			log.Fatalf("Failed to load lease rate limit configuration: %v", err)
		}
		log.Printf("Lease rate limit configuration loaded successfully (%d rules)", len(leaseRateLimitConfig.ListRules()))
	} else {
		log.Println("No lease rate limit configuration provided, using defaults")
		leaseRateLimitConfig = middleware.NewLeaseRateLimitConfig(50, 100)
	}

	// Load quota configuration if provided
	var quotaManager *quota.Manager
	if *quotaConfigPath != "" {
		log.Printf("Loading quota configuration from: %s", *quotaConfigPath)
		quotaManager, err = config.LoadQuotaConfig(*quotaConfigPath)
		if err != nil {
			log.Fatalf("Failed to load quota configuration: %v", err)
		}
		log.Printf("Quota configuration loaded successfully (%d limits)", len(quotaManager.ListLimits()))
		defer quotaManager.Close()
	} else {
		log.Println("No quota configuration provided, using default in-memory storage")
		// Create default quota manager with SQLite storage
		storage, err := quota.NewSQLiteStorage("quota.db")
		if err != nil {
			log.Fatalf("Failed to create quota storage: %v", err)
		}
		quotaManager = quota.NewManager(storage, 1000000, 107374182400, 100)
		defer quotaManager.Close()
	}

	// Create server
	server := NewServer(*port, *httpsPort, authConfig, tlsConfig, tlsEnabled, leaseRateLimitConfig, quotaManager)

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// NewServer creates a new relay server instance
func NewServer(port, httpsPort string, authConfig *middleware.AuthConfig, tlsConfig *tls.Config, tlsEnabled bool, leaseRateLimitConfig *middleware.LeaseRateLimitConfig, quotaManager *quota.Manager) *Server {
	mux := http.NewServeMux()

	// Create ACL configuration
	aclConfig := middleware.NewACLConfig()

	// Create base rate limit configuration (for admin and auth endpoints)
	// 100 req/s global, 50 req/s per API key, 10 req/s per IP
	baseRateLimitConfig := middleware.NewRateLimitConfig(100, 200)
	baseRateLimitConfig.PerKeyRequestsPerSecond = 50
	baseRateLimitConfig.PerKeyBurstSize = 100
	baseRateLimitConfig.PerIPRequestsPerSecond = 10
	baseRateLimitConfig.PerIPBurstSize = 20

	// Create middlewares
	authMiddleware := middleware.NewAuthMiddleware(authConfig)
	aclMiddleware := middleware.NewACLMiddleware(aclConfig)
	baseRateLimitMiddleware := middleware.NewRateLimitMiddleware(baseRateLimitConfig)

	// Create lease-specific rate limit middleware (for peer endpoints)
	leaseRateLimitMiddleware := middleware.NewLeaseRateLimitMiddleware(leaseRateLimitConfig, baseRateLimitConfig)

	// Create quota middleware
	quotaMiddleware := quota.NewQuotaMiddleware(quotaManager)

	// Create metrics middleware
	metricsMiddleware := metrics.NewMetricsMiddleware(metrics.GetDefaultMetrics())

	// Create logging middleware
	loggingMiddleware := logging.NewLoggingMiddleware(logging.Default())

	// Create circuit breaker middleware
	// 3 max requests in half-open, 30s timeout, 5 consecutive failures to trip
	circuitBreakerConfig := &circuitbreaker.MiddlewareConfig{
		MaxRequests:      3,
		Timeout:          30 * time.Second,
		FailureThreshold: 5,
	}
	circuitBreakerMiddleware := circuitbreaker.NewMiddleware(circuitBreakerConfig)

	// Create timeout middleware
	// Default 30s, MCP 10s, n8n 60s, OpenAI 30s
	timeoutConfig := timeout.DefaultMiddlewareConfig()
	timeoutMiddleware := timeout.NewMiddleware(timeoutConfig)

	// Create streaming middleware
	// Enable SSE and streaming support
	streamingConfig := streaming.DefaultMiddlewareConfig()
	streamingMiddleware := streaming.NewMiddleware(streamingConfig)

	// Create shutdown manager
	shutdownManager := shutdown.NewManager(nil)

	// Create admin handler
	adminHandler := NewAdminHandler(aclConfig, quotaManager)

	// Public endpoints (no authentication required)
	mux.HandleFunc("/health", makeHealthHandler(shutdownManager))
	mux.HandleFunc("/", handleRoot)
	mux.Handle("/metrics", promhttp.Handler()) // Prometheus metrics endpoint

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
	adminMux.HandleFunc("/admin/quota/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reset") && r.Method == http.MethodPost {
			adminHandler.HandleResetQuota(w, r)
		} else if r.Method == http.MethodGet {
			adminHandler.HandleGetQuotaStatus(w, r)
		} else if r.Method == http.MethodPost {
			adminHandler.HandleSetQuotaLimit(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Apply auth and base rate limit middleware to admin routes
	mux.Handle("/admin/", authMiddleware.Middleware(baseRateLimitMiddleware.Middleware(adminMux)))

	// Protected endpoints (authentication + ACL + timeout + circuit breaker + quota + lease-specific rate limiting + streaming required)
	peerMux := http.NewServeMux()
	peerMux.HandleFunc("/peer/", handlePeerRequest)

	// Apply auth, ACL, timeout, circuit breaker, quota, lease-specific rate limit, and streaming middleware to peer routes
	// Order: auth -> ACL (sets lease ID) -> timeout -> circuit breaker -> quota -> lease rate limit -> streaming -> handler
	mux.Handle("/peer/", authMiddleware.Middleware(aclMiddleware.Middleware(timeoutMiddleware.Middleware(circuitBreakerMiddleware.Middleware(quotaMiddleware.Middleware(leaseRateLimitMiddleware.Middleware(streamingMiddleware.Middleware(peerMux))))))))

	// Auth validation endpoint (authentication + base rate limiting only, no ACL)
	authValidateMux := http.NewServeMux()
	authValidateMux.HandleFunc("/auth/validate", handleAuthValidate)
	mux.Handle("/auth/validate", authMiddleware.Middleware(baseRateLimitMiddleware.Middleware(authValidateMux)))

	// Wrap all routes with middleware layers
	// Order: logging -> metrics -> routes
	metricsHandler := metricsMiddleware.Middleware(mux)
	loggingHandler := loggingMiddleware.Middleware(metricsHandler)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      loggingHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Create HTTPS server if TLS is enabled
	var httpsServer *http.Server
	if tlsEnabled && tlsConfig != nil {
		httpsServer = &http.Server{
			Addr:         ":" + httpsPort,
			Handler:      loggingHandler,
			TLSConfig:    tlsConfig,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
	}

	// Register servers with shutdown manager
	shutdownManager.RegisterServer(httpServer)
	if httpsServer != nil {
		shutdownManager.RegisterServer(httpsServer)
	}

	// Register cleanup functions
	shutdownManager.RegisterCleanup(func() error {
		quotaManager.Close()
		return nil
	})

	return &Server{
		httpServer:      httpServer,
		httpsServer:     httpsServer,
		authConfig:      authConfig,
		aclConfig:       aclConfig,
		tlsEnabled:      tlsEnabled,
		shutdownManager: shutdownManager,
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

	// Use shutdown manager for graceful shutdown
	log.Println("Shutting down servers...")
	if err := s.shutdownManager.Shutdown(ctx); err != nil {
		shutdown <- fmt.Errorf("shutdown error: %w", err)
		return
	}

	log.Println("Server shutdown completed successfully")
	shutdown <- nil
}

// makeHealthHandler creates a health check handler that returns 503 during shutdown
func makeHealthHandler(sm *shutdown.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Return 503 if shutting down
		if sm.IsShuttingDown() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"status":"shutting_down","timestamp":"%s"}`, time.Now().Format(time.RFC3339))
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"healthy","timestamp":"%s"}`, time.Now().Format(time.RFC3339))
	}
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
