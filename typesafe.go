package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	typeSafeAPIEndpoint      = "https://api.typesafe.ai/v1/systemone"
	defaultTypeSafeModel     = "jev-1.13.0"
	typeSafeRequestTimeout   = 30 * time.Second
	typeSafeMaxInputBytes    = 64 << 10
	typeSafeMaxResponseBytes = 1 << 20
	typeSafeMaxQuestions     = 32
)

type TypeSafeErrorKind string

const (
	TypeSafeErrorAuthentication TypeSafeErrorKind = "authentication"
	TypeSafeErrorRetryable      TypeSafeErrorKind = "retryable"
	TypeSafeErrorMalformed      TypeSafeErrorKind = "malformed"
	TypeSafeErrorTerminal       TypeSafeErrorKind = "terminal"
)

type TypeSafeError struct {
	Kind       TypeSafeErrorKind
	Operation  string
	StatusCode int
	Detail     string
}

func (e *TypeSafeError) Error() string {
	message := "typesafe " + e.Operation + " failed: " + string(e.Kind)
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.StatusCode)
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

type TypeSafeQuestion struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type TypeSafeRequest struct {
	State     any                         `json:"state"`
	Model     string                      `json:"model"`
	Questions map[string]TypeSafeQuestion `json:"questions"`
}

type TypeSafeAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

type TypeSafeResponse struct {
	Model   string                    `json:"model"`
	Answers map[string]TypeSafeAnswer `json:"answers"`
	Usage   TypeSafeUsage             `json:"usage"`
}

type TypeSafeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type TypeSafeClient struct {
	token      string
	model      string
	endpoint   string
	httpClient *http.Client
}

type TypeSafeClientConfig struct {
	Model    string
	Endpoint string
}

func NewTypeSafeClient(token string, transport http.RoundTripper, config TypeSafeClientConfig) (*TypeSafeClient, error) {
	if strings.TrimSpace(token) == "" {
		return nil, typeSafeMalformed("configure client", "token is required")
	}
	if transport == nil {
		return nil, typeSafeMalformed("configure client", "HTTP transport is required")
	}
	model := strings.TrimSpace(config.Model)
	if !safeProviderIdentifier(model) {
		return nil, typeSafeMalformed("configure client", "model is invalid")
	}
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = typeSafeAPIEndpoint
	}
	if err := validateTypeSafeEndpoint(endpoint); err != nil {
		return nil, typeSafeMalformed("configure client", "endpoint is invalid")
	}
	return &TypeSafeClient{
		token: token, model: model, endpoint: endpoint,
		httpClient: &http.Client{
			Transport: transport, Timeout: typeSafeRequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}
func validateTypeSafeEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.User != nil || parsed.Scheme != "https" || parsed.Host != "api.typesafe.ai" || parsed.Path != "/v1/systemone" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid endpoint")
	}
	return nil
}

func (c *TypeSafeClient) Evaluate(ctx context.Context, state any, questions map[string]TypeSafeQuestion) (TypeSafeResponse, error) {
	if c == nil || c.httpClient == nil {
		return TypeSafeResponse{}, typeSafeMalformed("evaluate", "client is not configured")
	}
	if len(questions) == 0 || len(questions) > typeSafeMaxQuestions {
		return TypeSafeResponse{}, typeSafeMalformed("evaluate", "question count is invalid")
	}
	for id, question := range questions {
		if !safeQuestionIdentifier(id) || (question.Type != "noul" && question.Type != "choice" && question.Type != "score") || question.Instructions == nil {
			return TypeSafeResponse{}, typeSafeMalformed("evaluate", "question is invalid")
		}
		if question.Type == "choice" || question.Type == "score" {
			if question.Criteria == nil {
				return TypeSafeResponse{}, typeSafeMalformed("evaluate", "question criteria is required")
			}
		}
	}
	payload := TypeSafeRequest{State: state, Model: c.model, Questions: questions}
	body, err := json.Marshal(payload)
	if err != nil || len(body) > typeSafeMaxInputBytes {
		return TypeSafeResponse{}, typeSafeMalformed("evaluate", "request is invalid or too large")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return TypeSafeResponse{}, typeSafeMalformed("evaluate", "request cannot be built")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return TypeSafeResponse{}, &TypeSafeError{Kind: TypeSafeErrorRetryable, Operation: "evaluate"}
	}
	defer ignoreCloseError(response.Body)
	if response.StatusCode != http.StatusOK {
		discardTypeSafeResponse(response.Body)
		return TypeSafeResponse{}, classifyTypeSafeStatus(response.StatusCode)
	}
	responseBody, err := readTypeSafeResponse(response.Body)
	if err != nil {
		return TypeSafeResponse{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	var result TypeSafeResponse
	if err := decoder.Decode(&result); err != nil {
		return TypeSafeResponse{}, typeSafeMalformed("evaluate", "success response is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return TypeSafeResponse{}, typeSafeMalformed("evaluate", "success response has trailing data")
	}
	if result.Model != c.model || result.Answers == nil || len(result.Answers) != len(questions) {
		return TypeSafeResponse{}, typeSafeMalformed("evaluate", "response answers are incomplete")
	}
	for id, question := range questions {
		answer, ok := result.Answers[id]
		if !ok || !validTypeSafeAnswer(question.Type, question.Criteria, answer) {
			return TypeSafeResponse{}, typeSafeMalformed("evaluate", "response answer is invalid")
		}
	}
	return result, nil
}

func validTypeSafeAnswer(questionType string, criteria any, answer TypeSafeAnswer) bool {
	if answer.Type != questionType {
		return false
	}
	switch questionType {
	case "noul":
		return answer.Noul != nil && validProbability(*answer.Noul)
	case "choice":
		if answer.Choice == "" || answer.Confidence == nil || !validProbability(*answer.Confidence) || len(answer.Probabilities) == 0 {
			return false
		}
		for option, probability := range answer.Probabilities {
			if option == "" || !validProbability(probability) {
				return false
			}
		}
		_, ok := answer.Probabilities[answer.Choice]
		return ok && typeSafeChoiceAllowed(criteria, answer.Choice)
	case "score":
		if answer.Score == nil || answer.Confidence == nil || !validProbability(*answer.Confidence) || len(answer.Probabilities) == 0 {
			return false
		}
		for level, probability := range answer.Probabilities {
			if level == "" || !validProbability(probability) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validProbability(value float64) bool { return value >= 0 && value <= 1 }
func typeSafeChoiceAllowed(criteria any, choice string) bool {
	switch values := criteria.(type) {
	case map[string]string:
		_, ok := values[choice]
		return ok
	case map[string]any:
		_, ok := values[choice]
		return ok
	default:
		return false
	}
}

func safeQuestionIdentifier(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func classifyTypeSafeStatus(status int) error {
	kind := TypeSafeErrorTerminal
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = TypeSafeErrorAuthentication
	case status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status == 529 || status >= 500:
		kind = TypeSafeErrorRetryable
	}
	return &TypeSafeError{Kind: kind, Operation: "evaluate", StatusCode: status}
}

func typeSafeMalformed(operation, detail string) error {
	return &TypeSafeError{Kind: TypeSafeErrorMalformed, Operation: operation, Detail: detail}
}

func readTypeSafeResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, typeSafeMaxResponseBytes+1))
	if err != nil {
		return nil, typeSafeMalformed("evaluate", "success response cannot be read")
	}
	if len(data) > typeSafeMaxResponseBytes {
		return nil, typeSafeMalformed("evaluate", "success response is too large")
	}
	return data, nil
}

func discardTypeSafeResponse(body io.Reader) {
	_, _ = io.CopyN(io.Discard, body, typeSafeMaxResponseBytes+1)
}
