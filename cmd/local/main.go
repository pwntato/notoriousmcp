package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/pwntato/notoriousmcp/internal/auth"
	"github.com/pwntato/notoriousmcp/internal/config"
	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/handlers"
	"github.com/pwntato/notoriousmcp/internal/store"
)

func main() {
	ctx := context.Background()

	tableName := envOrDefault("TABLE_NAME", "notoriousmcp")
	dynamoEndpoint := os.Getenv("DYNAMODB_ENDPOINT")
	dbClient, err := db.New(ctx, tableName, dynamoEndpoint)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	bucket := envOrDefault("S3_BUCKET", "notoriousmcp-local")
	s3Endpoint := os.Getenv("S3_ENDPOINT")
	storeClient, err := store.New(ctx, bucket, s3Endpoint)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	adminIDs := strings.Split(os.Getenv("ADMIN_GOOGLE_IDS"), ",")
	tokenSecret := []byte(envOrDefault("TOKEN_SECRET", "local-dev-secret-key-at-least-32!!"))
	authCfg := auth.Config{
		ClientID:       envOrDefault("GOOGLE_CLIENT_ID", "local-client-id"),
		ClientSecret:   envOrDefault("GOOGLE_CLIENT_SECRET", "local-client-secret"),
		RedirectURL:    envOrDefault("REDIRECT_URL", "http://localhost:3000/auth/callback"),
		AdminGoogleIDs: filterEmpty(adminIDs),
		TokenSecret:    tokenSecret,
		TrustProxy:     false,
	}

	authHandler := auth.New(authCfg, dbClient)
	mcpHandler := handlers.New(dbClient, storeClient, handlers.Config{
		DefaultStorageCap:  config.Int64EnvOrDefault("DEFAULT_STORAGE_CAP_BYTES", handlers.DefaultStorageCapBytes),
		DefaultTransferCap: config.Int64EnvOrDefault("DEFAULT_TRANSFER_CAP_BYTES", handlers.DefaultTransferCapBytes),
	})

	mux := http.NewServeMux()
	authHandler.RegisterRoutes(mux)
	mcpHandler.RegisterRoutes(mux)

	// Wrap the MCP endpoint with auth middleware.
	// Auth routes (/auth/*, /.well-known/*) are public.
	protected := auth.Middleware(authCfg, dbClient, mux)
	final := publicRouter(mux, protected)

	log.Println("listening on :3000")
	log.Fatal(http.ListenAndServe(":3000", final))
}

// publicRouter routes auth and well-known paths directly to mux (bypassing
// middleware), and all other paths through the auth-protected handler.
// auth.RegisterRoutes only registers /auth/login and /auth/callback (with
// trailing path components), so the /auth/ prefix check is correct — bare
// /auth with no trailing slash falls through to the protected handler and
// correctly returns 404.
func publicRouter(public, protected http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") ||
			strings.HasPrefix(r.URL.Path, "/.well-known/") ||
			r.URL.Path == "/register" {
			public.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// filterEmpty removes empty strings. Needed because strings.Split("", ",") returns
// [""] rather than [], so an unset ADMIN_GOOGLE_IDS env var must be cleaned before
// passing to auth.Config.
func filterEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
