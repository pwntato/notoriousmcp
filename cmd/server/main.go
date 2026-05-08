package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/pwntato/notoriousmcp/internal/auth"
	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/handlers"
	"github.com/pwntato/notoriousmcp/internal/store"
)

var handler http.Handler

func init() {
	ctx := context.Background()

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

	adminIDsRaw := ssmGet(mustEnv("SSM_ADMIN_GOOGLE_IDS"))
	tokenSecretRaw := ssmGet(mustEnv("SSM_TOKEN_SECRET"))
	authCfg := auth.Config{
		ClientID:       ssmGet(mustEnv("SSM_GOOGLE_CLIENT_ID")),
		ClientSecret:   ssmGet(mustEnv("SSM_GOOGLE_CLIENT_SECRET")),
		RedirectURL:    mustEnv("REDIRECT_URL"),
		AdminGoogleIDs: filterEmpty(strings.Split(adminIDsRaw, ",")),
		TokenSecret:    []byte(tokenSecretRaw),
		TrustProxy:     true,
	}

	authHandler := auth.New(authCfg, dbClient)
	mcpHandler := handlers.New(dbClient, storeClient)

	mux := http.NewServeMux()
	authHandler.RegisterRoutes(mux)
	mcpHandler.RegisterRoutes(mux)

	protected := auth.Middleware(authCfg, dbClient, mux)
	handler = publicRouter(mux, protected)
}

func lambdaHandler(ctx context.Context, req events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	httpReq, err := toHTTPRequest(ctx, req)
	if err != nil {
		return events.LambdaFunctionURLResponse{StatusCode: 400}, nil
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httpReq)

	resp := rec.Result()
	headers := make(map[string]string, len(resp.Header))
	for k, vs := range resp.Header {
		headers[k] = strings.Join(vs, ", ")
	}

	return events.LambdaFunctionURLResponse{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       rec.Body.String(),
	}, nil
}

func main() {
	lambda.Start(lambdaHandler)
}

func toHTTPRequest(ctx context.Context, req events.LambdaFunctionURLRequest) (*http.Request, error) {
	url := "https://" + req.RequestContext.DomainName + req.RawPath
	if req.RawQueryString != "" {
		url += "?" + req.RawQueryString
	}

	body := strings.NewReader(req.Body)
	httpReq, err := http.NewRequestWithContext(ctx, req.RequestContext.HTTP.Method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

func publicRouter(public, protected http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/auth/") ||
			strings.HasPrefix(r.URL.Path, "/.well-known/") {
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
