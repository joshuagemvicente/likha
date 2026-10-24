package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestResponsesAPIKeyErrorsAreNotLabeledChatGPT(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("x-request-id", "req_123")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"overloaded"}}`)
	}))
	defer server.Close()
	p, _ := LookupProvider("openai")
	client, err := New(p.BaseURL, "gpt-5.5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = variantTestTransport{target, http.DefaultTransport}
	_, err = client.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil, nil)
	if err == nil {
		t.Fatal("HTTP 500 was reported as success")
	}
	if !strings.HasPrefix(err.Error(), "model HTTP 500") || strings.Contains(err.Error(), "ChatGPT") || !strings.Contains(err.Error(), "req_123") {
		t.Fatalf("error = %q, want a wire-neutral model HTTP error with the request ID", err)
	}
}
