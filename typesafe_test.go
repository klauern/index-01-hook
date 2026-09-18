package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTypeSafeClientRequestAndResponse(t *testing.T) {
	var seen TypeSafeRequest
	client, err := NewTypeSafeClient("typesafe-secret", roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != typeSafeAPIEndpoint || request.Header.Get("Authorization") != "Bearer typesafe-secret" {
			t.Fatalf("request target or authorization is incorrect")
		}
		if err := json.NewDecoder(request.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		return fixtureResponse(http.StatusOK, `{"model":"jev-1.13.0","answers":{"supported":{"type":"noul","noul":0.94}},"usage":{"input_tokens":12,"output_tokens":4}}`), nil
	}), TypeSafeClientConfig{Model: "jev-1.13.0"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Evaluate(context.Background(), map[string]string{"text": "Buy paper"}, map[string]TypeSafeQuestion{
		"supported": {Type: "noul", Instructions: "Is the item supported?"},
	})
	if err != nil || result.Answers["supported"].Noul == nil || *result.Answers["supported"].Noul != 0.94 {
		t.Fatalf("Evaluate() = %+v, %v", result, err)
	}
	if seen.Model != "jev-1.13.0" || seen.Questions["supported"].Type != "noul" {
		t.Fatalf("request = %+v", seen)
	}
}

func TestTypeSafeClientClassifiesTransportFailure(t *testing.T) {
	client, err := NewTypeSafeClient("typesafe-secret", roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}), TypeSafeClientConfig{Model: "jev-1.13.0"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(context.Background(), "state", map[string]TypeSafeQuestion{"q": {Type: "noul", Instructions: "question"}})
	var typed *TypeSafeError
	if !errors.As(err, &typed) || typed.Kind != TypeSafeErrorRetryable {
		t.Fatalf("error = %v, want retryable TypeSafeError", err)
	}
	if strings.Contains(err.Error(), "typesafe-secret") {
		t.Fatal("error exposed the token")
	}
}

func TestTypeSafeClientRejectsUnsafeConfiguration(t *testing.T) {
	for name, config := range map[string]TypeSafeClientConfig{
		"blank model":     {Model: " "},
		"unsafe model":    {Model: "jev model"},
		"unsafe endpoint": {Model: "jev-1.13.0", Endpoint: "http://localhost/systemone"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTypeSafeClient("typesafe-secret", http.DefaultTransport, config); err == nil {
				t.Fatal("accepted unsafe configuration")
			}
		})
	}
}

func TestTypeSafeVerificationDoesNotSendProjectIdentifier(t *testing.T) {
	client, err := NewTypeSafeClient("typesafe-secret", roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		if strings.Contains(string(body), "project-secret-id") {
			t.Fatalf("request sent a project identifier: %s", body)
		}
		return fixtureResponse(http.StatusOK, verificationFixtureResponse()), nil
	}), TypeSafeClientConfig{Model: "jev-1.13.0"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := typeSafeVerifier{client: client, enabled: true, aliasDescriptions: map[string]string{"work": "employment and office tasks"}}
	result, err := verifier.Verify(context.Background(), "Prepare the office report", QueuedItem{Kind: ItemKindTask, Title: "Prepare the office report", ProjectAlias: "work"}, []string{"work"})
	if err != nil || result.Decision != VerificationAccept {
		t.Fatalf("Verify() = %+v, %v", result, err)
	}
}

func verificationFixtureResponse() string {
	answers := map[string]any{}
	for _, key := range []string{"injection_detected", "item_present", "kind_supported", "title_supported", "content_supported", "date_supported", "priority_supported", "tags_supported", "route_supported"} {
		value := 0.99
		if key == "injection_detected" {
			value = 0.01
		}
		answers[key] = map[string]any{"type": "noul", "noul": value}
	}
	answers["route_alias"] = map[string]any{"type": "choice", "choice": "work", "probabilities": map[string]float64{"work": 0.99, "no_match": 0.01}, "confidence": 0.99}
	body, _ := json.Marshal(map[string]any{"model": "jev-1.13.0", "answers": answers, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
	return string(body)
}
