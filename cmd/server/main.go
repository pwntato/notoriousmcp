package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/pwntato/notoriousmcp/internal/auth"
	internalconfig "github.com/pwntato/notoriousmcp/internal/config"
	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/handlers"
	"github.com/pwntato/notoriousmcp/internal/store"
)

var handler http.Handler

// initHandler builds the global handler from environment + SSM. Invoked from main
// so tests can swap the handler without triggering AWS config loading.
func initHandler() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}

	ssmClient := ssm.NewFromConfig(awsCfg)
	ssmGet := func(name string) string {
		out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           aws.String(name),
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatalf("ssm get %s: %v", name, err)
		}
		return aws.ToString(out.Parameter.Value)
	}

	tableName := mustEnv("TABLE_NAME")
	dbClient, err := db.New(ctx, tableName, "")
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	bucket := mustEnv("S3_BUCKET")
	storeClient, err := store.New(ctx, bucket, "")
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	adminIDsRaw := ssmGet(mustEnv("SSM_ADMIN_IDS"))
	tokenSecretRaw := ssmGet(mustEnv("SSM_TOKEN_SECRET"))
	authCfg := auth.Config{
		Provider:         auth.OAuthProvider(os.Getenv("OAUTH_PROVIDER")),
		OktaDomain:       os.Getenv("OKTA_DOMAIN"),
		ClientID:         ssmGet(mustEnv("SSM_OAUTH_CLIENT_ID")),
		ClientSecret:     ssmGet(mustEnv("SSM_OAUTH_CLIENT_SECRET")),
		RedirectURL:      mustEnv("REDIRECT_URL"),
		AdminIDs:         filterEmpty(strings.Split(adminIDsRaw, ",")),
		TokenSecret:      []byte(tokenSecretRaw),
		AutoApproveUsers: os.Getenv("AUTO_APPROVE_USERS") == "true",
		TrustProxy:       true,
		PublicBaseURL:    os.Getenv("PUBLIC_BASE_URL"),
	}

	authHandler := auth.New(authCfg, dbClient)
	mcpHandler := handlers.New(dbClient, storeClient, handlers.Config{
		DefaultStorageCap:  internalconfig.Int64EnvOrDefault("DEFAULT_STORAGE_CAP_BYTES", handlers.DefaultStorageCapBytes),
		DefaultTransferCap: internalconfig.Int64EnvOrDefault("DEFAULT_TRANSFER_CAP_BYTES", handlers.DefaultTransferCapBytes),
	})

	mux := http.NewServeMux()
	authHandler.RegisterRoutes(mux)
	mcpHandler.RegisterRoutes(mux)

	protected := auth.Middleware(authCfg, dbClient, mux)
	handler = publicRouter(mux, protected)
}

func lambdaHandler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	httpReq, err := toHTTPRequest(ctx, req)
	if err != nil {
		log.Printf("toHTTPRequest: %v", err)
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 400,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{"error":"bad request"}`,
		}, nil
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httpReq)

	resp := rec.Result()
	// API Gateway v2 requires Set-Cookie values in a dedicated Cookies array;
	// collapsing them into a comma-joined Headers entry produces a single malformed cookie.
	setCookies := resp.Header.Values("Set-Cookie")
	headers := make(map[string]string, len(resp.Header))
	for k, vs := range resp.Header {
		if http.CanonicalHeaderKey(k) == "Set-Cookie" {
			continue
		}
		headers[k] = strings.Join(vs, ", ")
	}

	body := rec.Body.String()
	isBase64 := false
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "text/") && !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "application/problem+json") {
		body = base64.StdEncoding.EncodeToString(rec.Body.Bytes())
		isBase64 = true
	}

	return events.APIGatewayV2HTTPResponse{
		StatusCode:      resp.StatusCode,
		Headers:         headers,
		Cookies:         setCookies,
		Body:            body,
		IsBase64Encoded: isBase64,
	}, nil
}

func main() {
	initHandler()
	lambda.Start(lambdaHandler)
}

func toHTTPRequest(ctx context.Context, req events.APIGatewayV2HTTPRequest) (*http.Request, error) {
	url := "https://" + req.RequestContext.DomainName + req.RawPath
	if req.RawQueryString != "" {
		url += "?" + req.RawQueryString
	}

	var bodyReader io.Reader
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(decoded)
	} else {
		bodyReader = strings.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.RequestContext.HTTP.Method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	// API Gateway v2 delivers cookies in a dedicated array, not in the Cookie header.
	if len(req.Cookies) > 0 {
		httpReq.Header.Set("Cookie", strings.Join(req.Cookies, "; "))
	}
	return httpReq, nil
}

// publicRouter bypasses auth middleware for /auth/ and /.well-known/ paths.
// Both public and protected wrap the same underlying mux; the distinction is
// that protected passes requests through auth.Middleware first.
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

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func filterEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
