package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestToHTTPRequest_CookiesArrayBecomesCookieHeader(t *testing.T) {
	req := events.APIGatewayV2HTTPRequest{
		RawPath:        "/auth/callback",
		RawQueryString: "state=abc&code=xyz",
		Cookies:        []string{"oauth_nonce=NONCE123", "extra=value"},
		Headers:        map[string]string{"User-Agent": "test"},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			DomainName: "example.com",
			HTTP:       events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
		},
	}

	httpReq, err := toHTTPRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("toHTTPRequest: %v", err)
	}

	got, err := httpReq.Cookie("oauth_nonce")
	if err != nil {
		t.Fatalf("oauth_nonce cookie not found on request: %v", err)
	}
	if got.Value != "NONCE123" {
		t.Errorf("oauth_nonce value: got %q want %q", got.Value, "NONCE123")
	}
	if extra, err := httpReq.Cookie("extra"); err != nil || extra.Value != "value" {
		t.Errorf("extra cookie missing or wrong: %+v err=%v", extra, err)
	}
	if httpReq.URL.RawQuery != "state=abc&code=xyz" {
		t.Errorf("raw query not preserved: %q", httpReq.URL.RawQuery)
	}
}

func TestLambdaHandler_SetCookieGoesIntoCookiesArray(t *testing.T) {
	// Swap in a tiny handler that always sets two cookies.
	prev := handler
	t.Cleanup(func() { handler = prev })
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "a", Value: "1", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "b", Value: "2", Path: "/auth"})
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusFound)
	})

	resp, err := lambdaHandler(context.Background(), events.APIGatewayV2HTTPRequest{
		RawPath: "/auth/login",
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			DomainName: "example.com",
			HTTP:       events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
		},
	})
	if err != nil {
		t.Fatalf("lambdaHandler: %v", err)
	}

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status: got %d want %d", resp.StatusCode, http.StatusFound)
	}
	if len(resp.Cookies) != 2 {
		t.Fatalf("Cookies: got %d entries (%v), want 2 separate Set-Cookie values", len(resp.Cookies), resp.Cookies)
	}
	for _, c := range resp.Cookies {
		if strings.Contains(c, ", ") {
			t.Errorf("cookie entry was comma-joined instead of split: %q", c)
		}
	}
	if _, ok := resp.Headers["Set-Cookie"]; ok {
		t.Error("Set-Cookie must not appear in Headers map; API Gateway v2 requires the Cookies array")
	}
}

// Sanity check: a no-cookie handler still works and Cookies is empty/nil.
func TestLambdaHandler_NoCookies(t *testing.T) {
	prev := handler
	t.Cleanup(func() { handler = prev })
	handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	resp, err := lambdaHandler(context.Background(), events.APIGatewayV2HTTPRequest{
		RawPath: "/status",
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			DomainName: "example.com",
			HTTP:       events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET"},
		},
	})
	if err != nil {
		t.Fatalf("lambdaHandler: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d want 200", resp.StatusCode)
	}
	if len(resp.Cookies) != 0 {
		t.Errorf("expected no cookies, got %v", resp.Cookies)
	}
}
