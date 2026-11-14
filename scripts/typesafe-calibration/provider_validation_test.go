package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestResponseValidationPreservesRefusals checks each independently rejected field.
func TestResponseValidationPreservesRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*response)
		detail string
	}{
		{"model", func(r *response) { r.Model = "other" }, "response model or answer count is invalid"},
		{"answers", func(r *response) { r.Answers = nil }, "response model or answer count is invalid"},
		{"missing answer", func(r *response) { r.Answers = map[string]answer{"other": r.Answers["verdict"]} }, "response answer type is invalid"},
		{"type", func(r *response) { a := r.Answers["verdict"]; a.Type = "noul"; r.Answers["verdict"] = a }, "response answer type is invalid"},
		{"choice", func(r *response) { a := r.Answers["verdict"]; a.Choice = ""; r.Answers["verdict"] = a }, "choice answer is invalid"},
		{"confidence", func(r *response) { a := r.Answers["verdict"]; a.Confidence = nil; r.Answers["verdict"] = a }, "choice answer is invalid"},
		{"confidence range", func(r *response) { a := r.Answers["verdict"]; a.Confidence = ptr(1.1); r.Answers["verdict"] = a }, "choice answer is invalid"},
		{"empty probabilities", func(r *response) { a := r.Answers["verdict"]; a.Probabilities = nil; r.Answers["verdict"] = a }, "choice answer is invalid"},
		{"not allowed", func(r *response) { a := r.Answers["verdict"]; a.Choice = "unknown"; r.Answers["verdict"] = a }, "choice is not allowed"},
		{"chosen missing", func(r *response) { a := r.Answers["verdict"]; a.Choice = "wrong_field"; r.Answers["verdict"] = a }, "chosen probability is missing"},
		{"supported missing", func(r *response) {
			a := r.Answers["verdict"]
			a.Choice = "wrong_field"
			a.Probabilities = map[string]float64{"wrong_field": 1}
			r.Answers["verdict"] = a
		}, "supported probability is missing"},
		{"probability range", func(r *response) { r.Answers["verdict"].Probabilities["supported"] = -.1 }, "probability is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := response{Model: "m", Answers: map[string]answer{"verdict": {Type: "choice", Choice: "supported", Confidence: ptr(.9), Probabilities: map[string]float64{"supported": .9}}}}
			tc.mutate(&r)
			err := (&client{model: "m"}).validateResponse(r, choiceQuestions())
			assertProviderDetail(t, err, tc.detail)
		})
	}
}

func assertProviderDetail(t *testing.T, err error, detail string) {
	t.Helper()
	pe, ok := err.(*providerError)
	if !ok {
		t.Fatalf("error=%v, want provider error", err)
	}
	if pe.Kind != "malformed" {
		t.Fatalf("kind=%q", pe.Kind)
	}
	if pe.Detail != detail {
		t.Fatalf("detail=%q, want %q", pe.Detail, detail)
	}
}

func TestResponseDecodeRejectsTrailingAndUnknownData(t *testing.T) {
	for _, body := range []string{`{} {}`, `{"unexpected":true}`} {
		t.Run(body, func(t *testing.T) {
			_, err := decodeResponse(&http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
			if err == nil {
				t.Fatal("malformed response was accepted")
			}
		})
	}
}

func TestResponseStatusClassification(t *testing.T) {
	cases := map[int]string{400: "terminal", 401: "authentication", 403: "authentication", 404: "terminal", 408: "retryable", 425: "retryable", 429: "retryable", 500: "retryable", 529: "retryable", 503: "retryable"}
	for status, want := range cases {
		got := responseError(status).(*providerError)
		if got.Kind != want {
			t.Errorf("HTTP %d kind=%q, want %q", status, got.Kind, want)
		}
	}
}
