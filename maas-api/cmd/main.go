package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/opendatahub-io/maas-billing/maas-api/internal/auth"
	"github.com/opendatahub-io/maas-billing/maas-api/internal/config"
	"github.com/opendatahub-io/maas-billing/maas-api/internal/handlers"
	"github.com/opendatahub-io/maas-billing/maas-api/internal/keys"
	"github.com/opendatahub-io/maas-billing/maas-api/internal/models"
	"github.com/opendatahub-io/maas-billing/maas-api/internal/teams"
	"github.com/opendatahub-io/maas-billing/maas-api/internal/tier"
	"github.com/opendatahub-io/maas-billing/maas-api/internal/token"

	v2auth "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/auth"
	v2config "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/config"
	v2db "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/db"
	v2handlers "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/handlers"
	v2keys "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/keys"
	v2metrics "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/metrics"
	v2models "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/models"
	v2teams "github.com/opendatahub-io/maas-billing/maas-api/v2/internal/teams"
)

func main() {
	cfg := config.Load()
	flag.Parse()

	gin.SetMode(gin.ReleaseMode) // Explicitly set release mode
	if cfg.DebugMode {
		gin.SetMode(gin.DebugMode)
	}

	router := gin.Default()
	if cfg.DebugMode {
		router.Use(cors.New(cors.Config{
			AllowMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders:  []string{"Authorization", "Content-Type", "Accept"},
			ExposeHeaders: []string{"Content-Type"},
			AllowOriginFunc: func(origin string) bool {
				return true
			},
			AllowCredentials: true,
			MaxAge:           12 * time.Hour,
		}))
	}

	router.OPTIONS("/*path", func(c *gin.Context) { c.Status(204) })

	ctx, cancel := context.WithCancel(context.Background())

	registerHandlers(ctx, router, cfg)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("Server starting on port %s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutdown signal received, shutting down server...")

	cancel()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exited gracefully")
}

func registerHandlers(ctx context.Context, router *gin.Engine, cfg *config.Config) {
	router.GET("/health", handlers.NewHealthHandler().HealthCheck)

	clusterConfig, err := config.NewClusterConfig()
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	modelMgr := models.NewManager(clusterConfig.DynClient)
	modelsHandler := handlers.NewModelsHandler(modelMgr)
	router.GET("/models", modelsHandler.ListModels)
	router.GET("/v1/models", modelsHandler.ListLLMs)

	switch cfg.Provider {
	case config.Secrets:
		configureSecretsProvider(cfg, router, clusterConfig)
	case config.SATokens:
		configureSATokenProvider(ctx, cfg, router, clusterConfig)
	case config.Database:
		configureDatabaseProvider(cfg, router, clusterConfig)
	default:
		log.Fatalf("Invalid provider: %s. Available providers: [secrets, sa-tokens, database]", cfg.Provider)
	}

}

func configureSATokenProvider(ctx context.Context, cfg *config.Config, router *gin.Engine, clusterConfig *config.K8sClusterConfig) {
	// V1 API routes
	v1Routes := router.Group("/v1")

	tierMapper := tier.NewMapper(clusterConfig.ClientSet, cfg.Name, cfg.Namespace)
	tierHandler := tier.NewHandler(tierMapper)
	v1Routes.POST("/tiers/lookup", tierHandler.TierLookup)

	informerFactory := informers.NewSharedInformerFactory(clusterConfig.ClientSet, 30*time.Second)

	namespaceInformer := informerFactory.Core().V1().Namespaces()
	serviceAccountInformer := informerFactory.Core().V1().ServiceAccounts()
	informersSynced := []cache.InformerSynced{
		namespaceInformer.Informer().HasSynced,
		serviceAccountInformer.Informer().HasSynced,
	}

	informerFactory.Start(ctx.Done())

	if !cache.WaitForNamedCacheSync("maas-api", ctx.Done(), informersSynced...) {
		log.Fatalf("Failed to sync informer caches")
	}

	manager := token.NewManager(
		cfg.Name,
		tierMapper,
		clusterConfig.ClientSet,
		namespaceInformer.Lister(),
		serviceAccountInformer.Lister(),
	)
	tokenHandler := token.NewHandler(cfg.Name, manager)

	tokenRoutes := v1Routes.Group("/tokens", token.ExtractUserInfo(token.NewReviewer(clusterConfig.ClientSet)))
	tokenRoutes.POST("", tokenHandler.IssueToken)
	tokenRoutes.DELETE("", tokenHandler.RevokeAllTokens)
}

func configureSecretsProvider(cfg *config.Config, router *gin.Engine, clusterConfig *config.K8sClusterConfig) {
	policyMgr := teams.NewPolicyManager(
		clusterConfig.DynClient,
		clusterConfig.ClientSet,
		cfg.KeyNamespace,
		cfg.TokenRateLimitPolicyName,
		cfg.AuthPolicyName,
	)

	teamMgr := teams.NewManager(clusterConfig.ClientSet, cfg.KeyNamespace, policyMgr)
	keyMgr := keys.NewManager(clusterConfig.ClientSet, cfg.KeyNamespace, teamMgr)

	usageHandler := handlers.NewUsageHandler(clusterConfig.ClientSet, clusterConfig.RestConfig, cfg.KeyNamespace)
	teamsHandler := handlers.NewTeamsHandler(teamMgr)
	keysHandler := handlers.NewKeysHandler(keyMgr, teamMgr)

	if cfg.CreateDefaultTeam {
		if err := teamMgr.CreateDefaultTeam(); err != nil {
			log.Printf("Warning: Failed to create default team: %v", err)
		} else {
			log.Printf("Default team created successfully")
		}
	}

	// Team management endpoints
	teamRoutes := router.Group("/teams", auth.AdminAuthMiddleware())
	teamRoutes.POST("", teamsHandler.CreateTeam)
	teamRoutes.GET("", teamsHandler.ListTeams)
	teamRoutes.GET("/:team_id", teamsHandler.GetTeam)
	teamRoutes.PATCH("/:team_id", teamsHandler.UpdateTeam)
	teamRoutes.DELETE("/:team_id", teamsHandler.DeleteTeam)
	teamRoutes.POST("/:team_id/keys", keysHandler.CreateTeamKey)
	teamRoutes.GET("/:team_id/keys", keysHandler.ListTeamKeys)
	teamRoutes.GET("/:team_id/usage", usageHandler.GetTeamUsage)

	// User management endpoints
	userRoutes := router.Group("/users", auth.AdminAuthMiddleware())
	userRoutes.GET("/:user_id/keys", keysHandler.ListUserKeys)
	userRoutes.GET("/:user_id/usage", usageHandler.GetUserUsage)

	// Key management endpoints
	keyRoutes := router.Group("/keys", auth.AdminAuthMiddleware())
	keyRoutes.DELETE("/:key_name", keysHandler.DeleteTeamKey)
}

func configureDatabaseProvider(cfg *config.Config, router *gin.Engine, clusterConfig *config.K8sClusterConfig) {
	// Load v2 database-specific configuration
	v2cfg := v2config.Load()

	// Connect to database
	database, err := v2db.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Create database repository
	repo := v2db.NewRepository(database)

	// Build Prometheus client for usage endpoints
	promClient, err := v2metrics.NewClient(v2metrics.ClientConfig{
		BaseURL:            v2cfg.PrometheusURL,
		TokenPath:          v2cfg.PrometheusTokenPath,
		CAPath:             v2cfg.PrometheusCAPath,
		InsecureSkipVerify: v2cfg.PrometheusInsecureTLS,
		Timeout:            v2cfg.PrometheusTimeout,
	})
	if err != nil {
		log.Printf("Warning: Prometheus client disabled: %v", err)
	}

	// Initialize managers with database backend
	policyMgr := v2teams.NewPolicyManager(
		clusterConfig.DynClient,
		clusterConfig.ClientSet,
		v2cfg.KeyNamespace,
		v2cfg.TokenRateLimitPolicyName,
	)

	keyMgr := v2keys.NewManager(repo)
	modelMgr := v2models.NewManager(clusterConfig.DynClient)

	// Initialize handlers with v2 implementations
	usageHandler := v2handlers.NewUsageHandler(clusterConfig.ClientSet, clusterConfig.RestConfig, v2cfg.KeyNamespace, promClient, v2cfg.UsageDefaultRange, v2cfg.PrometheusDebug)
	teamsHandler := v2handlers.NewTeamsHandler(repo, policyMgr)
	keysHandler := v2handlers.NewKeysHandler(keyMgr, repo)
	modelsHandler := v2handlers.NewModelsHandler(modelMgr)
	healthHandler := v2handlers.NewHealthHandler()
	identityHandler := v2handlers.NewIdentityHandler(repo)

	// Health check endpoint (no auth required)
	router.GET("/health", healthHandler.HealthCheck)

	// API key introspection endpoint for Authorino (no auth required - called internally)
	router.POST("/introspect", identityHandler.Introspect)

	// Setup authenticated API routes with JWT context extraction
	authRoutes := router.Group("/", v2auth.JWTAuthMiddleware())

	// User endpoints (require maas-user or maas-admin role)
	userRoutes := authRoutes.Group("/", v2auth.UserContextMiddleware())

	// User self-service and profile
	userRoutes.GET("/profile", identityHandler.Profile)

	// User key management
	userRoutes.GET("/users/:user_id/keys", keysHandler.ListUserKeys)
	userRoutes.POST("/users/:user_id/keys", keysHandler.CreateUserKey)
	userRoutes.GET("/usage", usageHandler.GetNamespaceUsage)

	// Team management endpoints
	userRoutes.GET("/teams", teamsHandler.ListTeams)
	userRoutes.POST("/teams", teamsHandler.CreateTeam)
	userRoutes.GET("/teams/:team_id", teamsHandler.GetTeam)
	userRoutes.PATCH("/teams/:team_id", teamsHandler.UpdateTeam)
	userRoutes.DELETE("/teams/:team_id", teamsHandler.DeleteTeam)

	// Team membership management
	userRoutes.POST("/teams/:team_id/members", teamsHandler.AddTeamMember)
	userRoutes.GET("/teams/:team_id/members", teamsHandler.ListTeamMembers)
	userRoutes.DELETE("/teams/:team_id/members/:user_id", teamsHandler.RemoveTeamMember)

	// Team model grant management
	userRoutes.POST("/teams/:team_id/grants", teamsHandler.CreateModelGrant)

	// Team-scoped API key management
	userRoutes.POST("/teams/:team_id/keys", keysHandler.CreateTeamKey)
	userRoutes.GET("/teams/:team_id/keys", keysHandler.ListTeamKeys)
	userRoutes.DELETE("/keys/:key_name", keysHandler.DeleteAPIKey)

	// Model listing
	userRoutes.GET("/models", modelsHandler.ListModels)

	log.Printf("Database provider configured successfully")
}
