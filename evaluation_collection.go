package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type evaluationRemoteTask struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	Description string `json:"desc"`
	Kind        string `json:"kind"`
	ModifiedAt  string `json:"modifiedTime"`
}

// collectEvaluationObservations reads destinations without changing TickTick tasks.
// A failed project read leaves all trusted observations unchanged.
func collectEvaluationObservations(ctx context.Context, store *Store, client *TickTickClient) (int, error) {
	evidence, err := store.ListEvaluationCollectionTargets(ctx)
	if err != nil {
		return 0, err
	}
	wanted := map[string]string{}
	knownProjects := map[string][]string{}
	for _, recording := range evidence {
		for _, delivery := range recording.Deliveries {
			marker, err := tickTickMarker(recording.Fingerprint, delivery.ItemIndex)
			if err != nil {
				return 0, err
			}
			if prior, exists := wanted[delivery.TaskID]; exists && prior != marker {
				return 0, errors.New("evaluation delivery identity is ambiguous")
			}
			wanted[delivery.TaskID] = marker
			projects := []string{}
			if observation := delivery.Observation; observation != nil {
				if observation.Status == "verified" {
					projects = append(projects, observation.ProjectID)
				}
				projects = append(projects, observation.LastKnownProjectID)
			}
			projects = append(projects, delivery.ProjectID)
			seen := map[string]bool{}
			for _, project := range projects {
				if project != "" && !seen[project] {
					knownProjects[delivery.TaskID] = append(knownProjects[delivery.TaskID], project)
					seen[project] = true
				}
			}
		}
	}
	if len(wanted) == 0 {
		return 0, nil
	}
	projects, err := client.listProjects(ctx)
	if err != nil {
		return 0, errors.New("evaluation collection could not list projects")
	}
	projectIDs := map[string]bool{"inbox": true}
	for _, project := range projects {
		if !safeProviderIdentifier(project.ID) {
			return 0, errors.New("evaluation project has no ID")
		}
		if !project.Closed && !strings.HasPrefix(project.ID, "inbox") {
			projectIDs[project.ID] = true
		}
	}
	found := map[string][]evaluationRemoteTask{}
	orderedProjects := make([]string, 0, len(projectIDs))
	for project := range projectIDs {
		orderedProjects = append(orderedProjects, project)
	}
	sort.Strings(orderedProjects)
	for _, project := range orderedProjects {
		var data struct {
			Tasks []evaluationRemoteTask `json:"tasks"`
		}
		if err := evaluationGetJSON(ctx, client, "/project/"+url.PathEscape(project)+"/data", &data); err != nil || data.Tasks == nil {
			return 0, errors.New("evaluation collection could not read all projects")
		}
		for _, task := range data.Tasks {
			if _, exists := wanted[task.ID]; !exists {
				continue
			}
			if !safeProviderIdentifier(task.ProjectID) {
				return 0, errors.New("evaluation task has no project ID")
			}
			// Inbox may use an account-specific ID in task responses.
			if task.ProjectID != project && !(project == "inbox" && strings.HasPrefix(task.ProjectID, "inbox")) {
				return 0, errors.New("evaluation task project conflicts with project response")
			}
			found[task.ID] = append(found[task.ID], task)
		}
	}
	observedAt := time.Now().UTC()
	observations := make([]EvaluationObservation, 0, len(wanted))
	for id, marker := range wanted {
		tasks := found[id]
		// Project task lists can omit completed tasks. Prefer the last known destination.
		if len(tasks) == 0 {
			for _, project := range knownProjects[id] {
				var task evaluationRemoteTask
				err := evaluationGetJSON(ctx, client, "/project/"+url.PathEscape(project)+"/task/"+url.PathEscape(id), &task)
				if err == nil && task.ID == id && task.ProjectID != "" {
					tasks = append(tasks, task)
					break
				} else if err != nil {
					var apiErr *TickTickError
					if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
						return 0, errors.New("evaluation collection could not verify missing task")
					}
				}
			}
		}
		observation := EvaluationObservation{TaskID: id, Marker: marker, ObservedAt: observedAt, Status: "missing"}
		if len(tasks) > 1 {
			observation.Status = "ambiguous"
		}
		if len(tasks) == 1 {
			task := tasks[0]
			observation.Status = "ambiguous"
			if strings.Count(task.Content+"\n"+task.Description, "[index01:") == 1 && (hasExactMarkerLine(task.Content, marker) || hasExactMarkerLine(task.Description, marker)) {
				observation.Status = "verified"
				observation.ProjectID, observation.Kind = task.ProjectID, task.Kind
				observation.Title, observation.ModifiedAt = task.Title, task.ModifiedAt
			}
		}
		observations = append(observations, observation)
	}
	if err := store.SaveEvaluationObservations(ctx, observations); err != nil {
		return 0, err
	}
	return len(observations), nil
}

func evaluationGetJSON(ctx context.Context, client *TickTickClient, path string, destination any) error {
	request, err := client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return errors.New("evaluation read request is invalid")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return errors.New("evaluation read request failed")
	}
	defer ignoreCloseError(response.Body)
	if response.StatusCode != http.StatusOK {
		discardResponse(response.Body)
		return classifyTickTickStatus("evaluation read", response.StatusCode)
	}
	if err := decodeTickTickJSON(response.Body, destination); err != nil {
		return errors.New("evaluation response is invalid")
	}
	return nil
}

func runEvaluationCollection(ctx context.Context, store *Store, client *TickTickClient, retention, interval time.Duration, logger *slog.Logger) {
	if retention <= 0 {
		interval = 0
	} else {
		logger.Info("evaluation capture enabled", "retention_days", int(retention/(24*time.Hour)), "poll_interval", interval.String())
	}
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	var poll <-chan time.Time
	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		poll = ticker.C
	}
	collect := func() {
		if interval <= 0 {
			return
		}
		pollCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		count, err := collectEvaluationObservations(pollCtx, store, client)
		if err != nil {
			logger.Warn("evaluation collection failed; previous observations retained")
			return
		}
		logger.Info("evaluation collection finished", "observations", count)
	}
	purge := func() {
		if _, err := store.PurgeExpiredEvaluationEvidence(ctx); err != nil {
			logger.Warn("evaluation archive expiry failed")
		}
	}
	purge()
	collect()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			purge()
		case <-poll:
			collect()
		}
	}
}

func evaluationMarker(fingerprint string, itemIndex int) string {
	return fmt.Sprintf("[index01:%s:%d]", fingerprint, itemIndex)
}
