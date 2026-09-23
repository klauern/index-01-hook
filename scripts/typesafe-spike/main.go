package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var spikeClient = &http.Client{Timeout: 30 * time.Second}

type candidate struct {
	Transcript string         `json:"transcript"`
	Item       map[string]any `json:"item"`
	Fabricated bool           `json:"fabricated"`
}

type answerEnvelope struct {
	Answers map[string]struct {
		Noul float64 `json:"noul"`
	} `json:"answers"`
	Usage struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
}

func main() {
	candidates, err := loadCandidates()
	if err != nil {
		fatal(err)
	}
	report := map[string]any{
		"synthetic":        true,
		"candidate_count":  len(candidates),
		"fabricated_count": countFabricated(candidates),
		"provisional":      true,
		"model":            os.Getenv("INDEX01_TYPESAFE_MODEL"),
		"live":             false,
		"results":          []any{},
		"review_volume":    nil,
		"latency_ms":       nil,
		"token_usage":      nil,
	}
	if report["model"] == "" {
		report["model"] = "jev-1.13.0"
	}
	token := strings.TrimSpace(os.Getenv("INDEX01_TYPESAFE_TOKEN"))
	if token == "" || os.Getenv("INDEX01_TYPESAFE_SPIKE_APPROVED") != "true" {
		report["note"] = "No live request was made. Set INDEX01_TYPESAFE_SPIKE_APPROVED=true with a server-side token for the synthetic trial."
		printReport(report)
		return
	}
	results := make([]any, 0, len(candidates))
	var supported, fabricated []float64
	reviewCount := 0
	var totalLatency, totalInput, totalOutput int64
	for _, item := range candidates {
		started := time.Now()
		answer, err := evaluate(token, fmt.Sprint(report["model"]), item)
		if err != nil {
			fatal(err)
		}
		unsupported := 1 - answer.Answers["supported"].Noul
		latency := time.Since(started).Milliseconds()
		totalLatency += latency
		totalInput += int64(answer.Usage.Input)
		totalOutput += int64(answer.Usage.Output)
		if unsupported >= 0.20 && unsupported < 0.90 {
			reviewCount++
		}
		entry := map[string]any{"fabricated": item.Fabricated, "unsupported_probability": unsupported, "latency_ms": time.Since(started).Milliseconds(), "input_tokens": answer.Usage.Input, "output_tokens": answer.Usage.Output}
		results = append(results, entry)
		if item.Fabricated {
			fabricated = append(fabricated, unsupported)
		} else {
			supported = append(supported, unsupported)
		}
	}
	report["review_volume"] = float64(reviewCount) / float64(len(candidates))
	report["latency_ms"] = totalLatency
	report["token_usage"] = map[string]int64{"input": totalInput, "output": totalOutput}
	report["live"] = true
	report["results"] = results
	report["supported_mean_unsupported"] = mean(supported)
	report["fabricated_mean_unsupported"] = mean(fabricated)
	report["separation"] = mean(fabricated) - mean(supported)
	report["note"] = "Synthetic fixture result only. This is a provisional signal, not a calibrated threshold."
	printReport(report)
}

func loadCandidates() ([]candidate, error) {
	var scenarios []struct {
		Turns []struct {
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Output json.RawMessage `json:"output"`
		} `json:"turns"`
	}
	data, err := os.ReadFile("testdata/evaluation/scenarios.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &scenarios); err != nil {
		return nil, err
	}
	var candidates []candidate
	for _, scenario := range scenarios {
		for _, turn := range scenario.Turns {
			var output struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(turn.Output, &output); err != nil {
				continue
			}
			for _, item := range output.Items {
				candidates = append(candidates, candidate{Transcript: turn.Message.Text, Item: item})
			}
		}
	}
	var corpus struct {
		Examples []struct {
			Input struct {
				Text string `json:"text"`
			} `json:"input"`
			SavedOutput struct {
				Items []map[string]any `json:"items"`
			} `json:"saved_output"`
		} `json:"examples"`
	}
	data, err = os.ReadFile("testdata/evaluation/feedback-corpus.json")
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		return nil, err
	}
	for _, example := range corpus.Examples {
		for _, item := range example.SavedOutput.Items {
			candidates = append(candidates, candidate{Transcript: example.Input.Text, Item: item})
		}
	}
	original := append([]candidate(nil), candidates...)
	for _, item := range original {
		fabricated := item
		fabricated.Fabricated = true
		fabricated.Item = clone(item.Item)
		if title, ok := fabricated.Item["title"].(string); ok {
			fabricated.Item["title"] = title + " with an invented detail"
		} else {
			fabricated.Item["content"] = fmt.Sprint(fabricated.Item["content"]) + " with an invented detail"
		}
		candidates = append(candidates, fabricated)
	}
	return candidates, nil
}

func evaluate(token, model string, item candidate) (answerEnvelope, error) {
	payload := map[string]any{"state": map[string]any{"transcript": item.Transcript, "candidate": item.Item}, "model": model, "questions": map[string]any{"supported": map[string]any{"type": "noul", "instructions": "Does the transcription support every candidate field without invented details?", "criteria": map[string]string{"true": "Supported", "false": "Invented or unsupported"}}}}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.typesafe.ai/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return answerEnvelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := spikeClient.Do(req)
	if err != nil {
		return answerEnvelope{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
		return answerEnvelope{}, fmt.Errorf("TypeSafe returned HTTP %d", res.StatusCode)
	}
	var answer answerEnvelope
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&answer); err != nil {
		return answerEnvelope{}, err
	}
	return answer, nil
}

func clone(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
func countFabricated(items []candidate) int {
	count := 0
	for _, item := range items {
		if item.Fabricated {
			count++
		}
	}
	return count
}
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
func printReport(report map[string]any) {
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
