package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	deepSeekTestToken    = "deepseek-test-token"
	deepSeekTestTimeZone = "America/Chicago"
)

func newDeepSeekFixture(t *testing.T, now time.Time, transport roundTripFunc) *DeepSeekClient {
	t.Helper()
	client, err := NewDeepSeekClientWithConfig(deepSeekTestToken, transport, func() time.Time { return now }, DeepSeekClientConfig{
		Model: deepSeekModel, TimeZone: deepSeekTestTimeZone,
	})
	if err != nil {
		t.Fatalf("NewDeepSeekClientWithConfig() error = %v", err)
	}
	return client
}

func deepSeekFixtureOutput(id, output string) *http.Response {
	encoded, _ := json.Marshal(output)
	body := fmt.Sprintf(`{"id":%q,"output":[{"type":"message","content":[{"type":"output_text","text":%s}]}]}`, id, encoded)
	return fixtureResponse(http.StatusOK, body)
}

func validDeepSeekTask(title string) string {
	return fmt.Sprintf(`{"kind":"task","title":%q,"content":"","due_at":null,"all_day":false,"priority":0,"tags":[],"project_alias":null}`, title)
}

func validDeepSeekNote(title, content string) string {
	return fmt.Sprintf(`{"kind":"note","title":%q,"content":%q}`, title, content)
}

func TestNewDeepSeekClientDefaultsToUTC(t *testing.T) {
	client, err := NewDeepSeekClient(deepSeekTestToken, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return deepSeekFixtureOutput("resp-default", `{"items":[]}`), nil
	}), time.Now)
	if err != nil {
		t.Fatalf("NewDeepSeekClient() error = %v", err)
	}
	if client.model != defaultDeepSeekModel || client.timeZone != "UTC" {
		t.Fatalf("client defaults = model:%q time-zone:%q", client.model, client.timeZone)
	}
}

func TestNewDeepSeekClientWithConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		config DeepSeekClientConfig
	}{
		{name: "blank model", config: DeepSeekClientConfig{TimeZone: "UTC"}},
		{name: "unsafe model", config: DeepSeekClientConfig{Model: "deepseek model", TimeZone: "UTC"}},
		{name: "blank time zone", config: DeepSeekClientConfig{Model: defaultDeepSeekModel}},
		{name: "local time zone", config: DeepSeekClientConfig{Model: defaultDeepSeekModel, TimeZone: "Local"}},
		{name: "unknown time zone", config: DeepSeekClientConfig{Model: defaultDeepSeekModel, TimeZone: "Mars/Olympus"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewDeepSeekClientWithConfig(deepSeekTestToken, http.DefaultTransport, time.Now, test.config)
			if err == nil {
				t.Fatal("NewDeepSeekClientWithConfig() accepted invalid settings")
			}
			for _, privateValue := range []string{deepSeekTestToken, test.config.Model, test.config.TimeZone} {
				if privateValue != "" && strings.Contains(err.Error(), privateValue) {
					t.Fatalf("configuration error contains private value %q: %v", privateValue, err)
				}
			}
		})
	}
}

func TestDeepSeekRequestUsesFixedResponsesContract(t *testing.T) {
	now := time.Date(2026, time.August, 12, 18, 0, 0, 0, time.UTC)
	transcript := "Buy milk tomorrow"
	client := newDeepSeekFixture(t, now, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != deepSeekResponsesURL {
			t.Fatalf("request = %s %s, want POST %s", request.Method, request.URL, deepSeekResponsesURL)
		}
		if request.Header.Get("Authorization") != "Bearer "+deepSeekTestToken {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q", request.Header.Get("Content-Type"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["model"] != deepSeekModel || body["max_output_tokens"] != float64(deepSeekMaxOutputTokens) {
			t.Errorf("model/output limit = (%v, %v)", body["model"], body["max_output_tokens"])
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 2 {
			t.Fatalf("input = %#v", body["input"])
		}
		system := input[0].(map[string]any)["content"].(string)
		user := input[1].(map[string]any)["content"].(string)
		for _, required := range []string{
			"2026-08-12",
			"The time zone is " + deepSeekTestTimeZone + ". Configured project aliases are: home, work.",
			"correct " + deepSeekTestTimeZone + " offset",
			"untrusted", "clear semantic match", "Client or office work",
			"Household chores or home maintenance", "Use null when no match is clear",
		} {
			if !strings.Contains(system, required) {
				t.Errorf("system prompt does not contain %q: %q", required, system)
			}
		}
		if user != transcript {
			t.Errorf("user content = %q, want exact transcript", user)
		}
		text := body["text"].(map[string]any)
		format := text["format"].(map[string]any)
		if format["type"] != "json_schema" || format["name"] != "index01_items" || format["strict"] != true {
			t.Errorf("schema format = %#v", format)
		}
		schema := format["schema"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Errorf("top-level additionalProperties = %#v", schema["additionalProperties"])
		}
		items := schema["properties"].(map[string]any)["items"].(map[string]any)
		if items["minItems"] != float64(0) || items["maxItems"] != float64(deepSeekMaxItems) {
			t.Errorf("item limits = %#v", items)
		}
		variants := items["items"].(map[string]any)["anyOf"].([]any)
		if len(variants) != 2 {
			t.Fatalf("item variants = %#v", variants)
		}
		taskSchema := variants[0].(map[string]any)
		noteSchema := variants[1].(map[string]any)
		if taskSchema["additionalProperties"] != false || noteSchema["additionalProperties"] != false {
			t.Errorf("variant strictness = (%v, %v)", taskSchema["additionalProperties"], noteSchema["additionalProperties"])
		}
		taskProperties := taskSchema["properties"].(map[string]any)
		noteProperties := noteSchema["properties"].(map[string]any)
		taskKind := taskProperties["kind"].(map[string]any)
		noteKind := noteProperties["kind"].(map[string]any)
		if taskKind["type"] != "string" || fmt.Sprint(taskKind["enum"]) != "[task]" ||
			noteKind["type"] != "string" || fmt.Sprint(noteKind["enum"]) != "[note]" {
			t.Errorf("kind schemas = (%#v, %#v)", taskProperties["kind"], noteProperties["kind"])
		}
		if _, exists := taskKind["const"]; exists {
			t.Error("task kind schema uses provider-incompatible const")
		}
		if _, exists := noteKind["const"]; exists {
			t.Error("note kind schema uses provider-incompatible const")
		}
		for _, field := range []string{"due_at", "all_day", "priority", "tags", "project_alias"} {
			if _, exists := noteProperties[field]; exists {
				t.Errorf("note schema contains task field %q", field)
			}
		}
		return deepSeekFixtureOutput("resp_1", `{"items":[]}`), nil
	})
	if client.httpClient.Timeout != deepSeekRequestTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", client.httpClient.Timeout, deepSeekRequestTimeout)
	}
	result, err := client.Extract(context.Background(), transcript, []string{"Work", "home"})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Provider != "deepseek" || result.Model != deepSeekModel || result.ProviderResponseID != "resp_1" || len(result.Items) != 0 {
		t.Errorf("result = %+v", result)
	}
}

func TestConfiguredDeepSeekClientUsesModelAndTimeZone(t *testing.T) {
	now := time.Date(2026, time.August, 12, 1, 0, 0, 0, time.UTC)
	var requestBody deepSeekRequest
	client, err := NewDeepSeekClientWithConfig(deepSeekTestToken, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != deepSeekResponsesURL {
			t.Fatalf("request URL = %s, want %s", request.URL, deepSeekResponsesURL)
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return deepSeekFixtureOutput("resp-configured", `{"items":[]}`), nil
	}), func() time.Time { return now }, DeepSeekClientConfig{
		Model:    "  deepseek-custom-v1  ",
		TimeZone: "  America/Los_Angeles  ",
	})
	if err != nil {
		t.Fatalf("NewDeepSeekClientWithConfig() error = %v", err)
	}
	if client.model != "deepseek-custom-v1" || client.timeZone != "America/Los_Angeles" {
		t.Fatalf("client settings = model:%q time-zone:%q", client.model, client.timeZone)
	}
	result, err := client.Extract(context.Background(), "Synthetic transcript", nil)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if requestBody.Model != "deepseek-custom-v1" {
		t.Errorf("request model = %q, want deepseek-custom-v1", requestBody.Model)
	}
	if len(requestBody.Input) == 0 || !strings.Contains(requestBody.Input[0].Content, "2026-08-11") || !strings.Contains(requestBody.Input[0].Content, "America/Los_Angeles") {
		t.Errorf("system prompt = %q", requestBody.Input[0].Content)
	}
	if result.Model != "deepseek-custom-v1" {
		t.Errorf("FrozenExtraction.Model = %q, want deepseek-custom-v1", result.Model)
	}
}

func TestDeepSeekExtractsTasksNotesAndMixedItems(t *testing.T) {
	tests := []struct {
		name   string
		output string
		kinds  []ItemKind
	}{
		{name: "zero", output: `{"items":[]}`},
		{name: "task", output: `{"items":[` + validDeepSeekTask("One") + `]}`, kinds: []ItemKind{ItemKindTask}},
		{name: "note", output: `{"items":[` + validDeepSeekNote("Idea", "Keep the full context") + `]}`, kinds: []ItemKind{ItemKindNote}},
		{name: "mixed", output: `{"items":[` + validDeepSeekTask("One") + `,` + validDeepSeekNote("Idea", "Details") + `]}`, kinds: []ItemKind{ItemKindTask, ItemKindNote}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newDeepSeekFixture(t, time.Now(), func(*http.Request) (*http.Response, error) {
				return deepSeekFixtureOutput("resp_1", test.output), nil
			})
			result, err := client.Extract(context.Background(), "Synthetic transcript", nil)
			if err != nil || len(result.Items) != len(test.kinds) {
				t.Fatalf("Extract() = %+v, %v, want %d items", result, err, len(test.kinds))
			}
			for index, kind := range test.kinds {
				if result.Items[index].Kind != kind {
					t.Errorf("item %d kind = %q, want %q", index, result.Items[index].Kind, kind)
				}
			}
		})
	}
}

func TestDeepSeekInterpretsCentralDatesConservatively(t *testing.T) {
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		due        string
		allDay     bool
		wantDate   string
		wantOffset int
	}{
		{name: "tomorrow", due: "2026-08-13", allDay: true, wantDate: "2026-08-13", wantOffset: -5 * 60 * 60},
		{name: "next weekday", due: "2026-08-17T09:00:00-05:00", wantDate: "2026-08-17", wantOffset: -5 * 60 * 60},
		{name: "before spring transition", due: "2026-03-08T01:30:00-06:00", wantDate: "2026-03-08", wantOffset: -6 * 60 * 60},
		{name: "after spring transition", due: "2026-03-08T03:30:00-05:00", wantDate: "2026-03-08", wantOffset: -5 * 60 * 60},
		{name: "first fall occurrence", due: "2026-11-01T01:30:00-05:00", wantDate: "2026-11-01", wantOffset: -5 * 60 * 60},
		{name: "second fall occurrence", due: "2026-11-01T01:30:00-06:00", wantDate: "2026-11-01", wantOffset: -6 * 60 * 60},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := fmt.Sprintf(`{"items":[{"kind":"task","title":"Date task","content":"","due_at":%q,"all_day":%t,"priority":0,"tags":[],"project_alias":null}]}`, test.due, test.allDay)
			client := newDeepSeekFixture(t, now, func(*http.Request) (*http.Response, error) {
				return deepSeekFixtureOutput("resp_1", output), nil
			})
			result, err := client.Extract(context.Background(), "Date request", nil)
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			due := result.Items[0].Due
			if due == nil || due.In(client.location).Format("2006-01-02") != test.wantDate {
				t.Fatalf("due = %v, want date %s", due, test.wantDate)
			}
			_, offset := due.Zone()
			if offset != test.wantOffset {
				t.Errorf("offset = %d, want %d", offset, test.wantOffset)
			}
		})
	}

	t.Run("vague date and time stay absent", func(t *testing.T) {
		client := newDeepSeekFixture(t, now, func(*http.Request) (*http.Response, error) {
			return deepSeekFixtureOutput("resp_1", `{"items":[`+validDeepSeekTask("Maybe later")+`]}`), nil
		})
		result, err := client.Extract(context.Background(), "Maybe do this sometime next week in the afternoon", nil)
		if err != nil || result.Items[0].Due != nil || result.Items[0].AllDay {
			t.Fatalf("vague Extract() = %+v, %v", result, err)
		}
	})
}

func TestDeepSeekRejectsInvalidStructuredOutput(t *testing.T) {
	tasks := make([]string, deepSeekMaxItems+1)
	for index := range tasks {
		tasks[index] = validDeepSeekTask(fmt.Sprintf("Task %d", index))
	}
	tests := []struct {
		name    string
		output  string
		aliases []string
	}{
		{name: "malformed JSON", output: `{`},
		{name: "unknown top field", output: `{"items":[],"secret":"raw"}`},
		{name: "unknown task field", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":null,"all_day":false,"priority":0,"tags":[],"project_alias":null,"extra":true}]}`},
		{name: "unknown note field", output: `{"items":[{"kind":"note","title":"Note","content":"Details","extra":true}]}`},
		{name: "missing kind", output: `{"items":[{"title":"Task","content":"","due_at":null,"all_day":false,"priority":0,"tags":[],"project_alias":null}]}`},
		{name: "unknown kind", output: `{"items":[{"kind":"event","title":"Event","content":"Details"}]}`},
		{name: "missing common field", output: `{"items":[{"kind":"note","title":"Note"}]}`},
		{name: "missing task field", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":null,"all_day":false,"priority":0,"project_alias":null}]}`},
		{name: "null required field", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":null,"all_day":false,"priority":0,"tags":null,"project_alias":null}]}`},
		{name: "task field on note", output: `{"items":[{"kind":"note","title":"Note","content":"Details","due_at":null}]}`},
		{name: "too many items", output: `{"items":[` + strings.Join(tasks, ",") + `]}`},
		{name: "blank title", output: `{"items":[{"kind":"task","title":" ","content":"","due_at":null,"all_day":false,"priority":0,"tags":[],"project_alias":null}]}`},
		{name: "invalid priority", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":null,"all_day":false,"priority":2,"tags":[],"project_alias":null}]}`},
		{name: "vague due value", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":"tomorrow","all_day":true,"priority":0,"tags":[],"project_alias":null}]}`},
		{name: "wrong summer offset", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":"2026-07-01T09:00:00-06:00","all_day":false,"priority":0,"tags":[],"project_alias":null}]}`},
		{name: "all day without due", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":null,"all_day":true,"priority":0,"tags":[],"project_alias":null}]}`},
		{name: "unknown alias", output: `{"items":[{"kind":"task","title":"Task","content":"","due_at":null,"all_day":false,"priority":0,"tags":[],"project_alias":"private"}]}`, aliases: []string{"work"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newDeepSeekFixture(t, time.Now(), func(*http.Request) (*http.Response, error) {
				return deepSeekFixtureOutput("resp_1", test.output), nil
			})
			_, err := client.Extract(context.Background(), "Private transcript", test.aliases)
			if deepSeekErrorKind(err) != DeepSeekErrorMalformed {
				t.Fatalf("Extract() error = %v, kind = %q", err, deepSeekErrorKind(err))
			}
		})
	}
}

func TestDeepSeekBoundsInputAndResponse(t *testing.T) {
	requests := 0
	client := newDeepSeekFixture(t, time.Now(), func(*http.Request) (*http.Response, error) {
		requests++
		return deepSeekFixtureOutput("resp_1", `{"items":[]}`), nil
	})
	_, err := client.Extract(context.Background(), strings.Repeat("x", deepSeekMaxInputBytes+1), nil)
	if deepSeekErrorKind(err) != DeepSeekErrorMalformed || requests != 0 {
		t.Fatalf("oversized input error = %v, requests = %d", err, requests)
	}

	client = newDeepSeekFixture(t, time.Now(), func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", deepSeekMaxResponseBytes+1))),
		}, nil
	})
	_, err = client.Extract(context.Background(), "Private transcript", nil)
	if deepSeekErrorKind(err) != DeepSeekErrorMalformed {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestDeepSeekClassifiesProviderOutcomesWithoutPrivateData(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		transport error
		want      DeepSeekErrorKind
	}{
		{name: "bad request", status: http.StatusBadRequest, body: `{"error":"raw provider body"}`, want: DeepSeekErrorTerminal},
		{name: "unauthorized", status: http.StatusUnauthorized, want: DeepSeekErrorAuthentication},
		{name: "forbidden", status: http.StatusForbidden, want: DeepSeekErrorAuthentication},
		{name: "timeout status", status: http.StatusRequestTimeout, want: DeepSeekErrorRetryable},
		{name: "too early", status: http.StatusTooEarly, want: DeepSeekErrorRetryable},
		{name: "rate limit", status: http.StatusTooManyRequests, want: DeepSeekErrorRetryable},
		{name: "server error", status: http.StatusBadGateway, want: DeepSeekErrorRetryable},
		{name: "context timeout", transport: context.DeadlineExceeded, want: DeepSeekErrorRetryable},
		{name: "network error", transport: errors.New("raw network detail"), want: DeepSeekErrorRetryable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newDeepSeekFixture(t, time.Now(), func(*http.Request) (*http.Response, error) {
				if test.transport != nil {
					return nil, test.transport
				}
				return fixtureResponse(test.status, test.body), nil
			})
			_, err := client.Extract(context.Background(), "private transcript", nil)
			if deepSeekErrorKind(err) != test.want {
				t.Fatalf("Extract() error = %v, kind = %q, want %q", err, deepSeekErrorKind(err), test.want)
			}
			var typed *DeepSeekError
			if !errors.As(err, &typed) {
				t.Fatalf("Extract() error type = %T, want *DeepSeekError", err)
			}
			for _, secret := range []string{deepSeekTestToken, "private transcript", "raw provider body", "raw network detail"} {
				if strings.Contains(fmt.Sprintf("%+v", *typed), secret) {
					t.Errorf("typed error contains %q: %+v", secret, *typed)
				}
			}
		})
	}

	t.Run("refusal requires review", func(t *testing.T) {
		client := newDeepSeekFixture(t, time.Now(), func(*http.Request) (*http.Response, error) {
			return fixtureResponse(http.StatusOK, `{"id":"resp_1","output":[{"type":"message","content":[{"type":"refusal","refusal":"raw refusal"}]}]}`), nil
		})
		_, err := client.Extract(context.Background(), "private transcript", nil)
		if deepSeekErrorKind(err) != DeepSeekErrorReview || strings.Contains(err.Error(), "raw refusal") {
			t.Fatalf("refusal error = %v", err)
		}
	})

	t.Run("malformed output excludes raw response", func(t *testing.T) {
		const rawResponse = `{"items":[{"kind":"note","title":"Private","content":"raw model response sentinel","due_at":null}]}`
		client := newDeepSeekFixture(t, time.Now(), func(*http.Request) (*http.Response, error) {
			return deepSeekFixtureOutput("resp_1", rawResponse), nil
		})
		_, err := client.Extract(context.Background(), "private transcript", nil)
		var typed *DeepSeekError
		if !errors.As(err, &typed) || typed.Kind != DeepSeekErrorMalformed {
			t.Fatalf("Extract() error = %v", err)
		}
		for _, secret := range []string{"private transcript", "raw model response sentinel", rawResponse} {
			if strings.Contains(fmt.Sprintf("%+v", *typed), secret) {
				t.Errorf("typed error contains %q: %+v", secret, *typed)
			}
		}
	})
}

func TestDeepSeekRequiresFixtureTransport(t *testing.T) {
	_, err := NewDeepSeekClient(deepSeekTestToken, nil, time.Now)
	if deepSeekErrorKind(err) != DeepSeekErrorMalformed {
		t.Fatalf("NewDeepSeekClient(nil transport) error = %v", err)
	}
}

func deepSeekErrorKind(err error) DeepSeekErrorKind {
	var typed *DeepSeekError
	if errors.As(err, &typed) {
		return typed.Kind
	}
	return ""
}
