package memorybridge

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestEndpointRequiresAttemptCredentialAndRevokesOnClose(t *testing.T) {
	endpoint, err := Start(context.Background(), func(_ context.Context, query string) (string, error) { return "lesson:" + query, nil })
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(endpoint.URL, "application/json", strings.NewReader(`{"query":"retries"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("memory endpoint accepted anonymous caller")
	}
	result, err := Search(context.Background(), endpoint.URL, endpoint.Token, "retries")
	if err != nil || result != "lesson:retries" {
		t.Fatalf("search %q %v", result, err)
	}
	endpoint.Close()
	if _, err = Search(context.Background(), endpoint.URL, endpoint.Token, "retries"); err == nil {
		t.Fatal("closed attempt still callable")
	}
}
