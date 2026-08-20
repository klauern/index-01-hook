package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

const (
	maxWebhookParts           = 4
	maxWebhookRecordedAtBytes = 20
	maxWebhookClientBytes     = 128
	maxWebhookTriggerBytes    = 128
	maxWebhookFilenameBytes   = 255
	maxWebhookPartHeaders     = 8
	maxWebhookPartHeaderBytes = 4 << 10
	maxWebhookHeaders         = 32
	maxWebhookHeaderBytes     = 16 << 10
	maxWebhookContentType     = 256
)

var errWebhookFieldTooLarge = errors.New("webhook field is too large")

type webhookRejectionError struct {
	reasonCode string
	cause      error
}

func (e *webhookRejectionError) Error() string {
	return e.cause.Error()
}

func (e *webhookRejectionError) Unwrap() error {
	return e.cause
}

func newWebhookRejectionError(reasonCode string, cause error) error {
	return &webhookRejectionError{reasonCode: reasonCode, cause: cause}
}

func webhookRejectionReason(err error) string {
	var rejection *webhookRejectionError
	if errors.As(err, &rejection) {
		return rejection.reasonCode
	}
	return "invalid_multipart"
}

type webhookServer struct {
	store        *Store
	token        string
	maxBodyBytes int64
	logger       *slog.Logger
}

type parsedWebhook struct {
	recordedAtMillis int64
	client           string
	trigger          string
	transcription    string
	audioPresent     bool
	audioFilename    string
	audioByteCount   int64
	audioDigest      string
}

func NewHandler(store *Store, token string, maxBodyBytes int64, logger *slog.Logger) http.Handler {
	server := &webhookServer{store: store, token: token, maxBodyBytes: maxBodyBytes, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", server.handleWebhook)
	mux.HandleFunc("GET /healthz", server.handleHealth)
	mux.HandleFunc("GET /statusz", server.handleOperationalStatus)
	mux.HandleFunc("GET /readyz", server.handleReadiness)
	return mux
}

func (s *webhookServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if err := validateWebhookHeaders(r); err != nil {
		s.rejectWebhook(w, http.StatusRequestHeaderFieldsTooLarge, "request_headers_invalid", "request headers are too large or invalid")
		return
	}
	if reasonCode := s.authorizationRejectionReason(r); reasonCode != "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="index01"`)
		s.rejectWebhook(w, http.StatusUnauthorized, reasonCode, "unauthorized")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		s.rejectWebhook(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be multipart/form-data")
		return
	}
	if r.ContentLength > s.maxBodyBytes {
		s.rejectWebhook(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	payload, err := parseWebhook(r)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.rejectWebhook(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return
		}
		if errors.Is(err, errWebhookFieldTooLarge) {
			s.rejectWebhook(w, http.StatusRequestEntityTooLarge, "field_too_large", "webhook field is too large")
			return
		}
		s.rejectWebhook(w, http.StatusBadRequest, webhookRejectionReason(err), "invalid webhook payload")
		return
	}

	fingerprint := fingerprintWebhook(payload)
	receipt, err := s.store.SaveRecording(r.Context(), RecordingInput{
		RecordedAtMillis: payload.recordedAtMillis,
		Client:           payload.client,
		Trigger:          payload.trigger,
		Transcription:    payload.transcription,
		AudioFilename:    payload.audioFilename,
		AudioByteCount:   payload.audioByteCount,
		Fingerprint:      fingerprint,
	})
	if err != nil {
		s.logger.Error("failed to persist webhook", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to persist webhook")
		return
	}
	status := http.StatusAccepted
	if receipt.Duplicate {
		status = http.StatusOK
	}
	s.logger.Info("recorded webhook", "recording_id", receipt.ID, "duplicate", receipt.Duplicate, "queued", receipt.Queued)
	writeJSON(w, status, receipt)
}

func (s *webhookServer) authorizationRejectionReason(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "missing_authorization"
	}
	if len(values) != 1 {
		return "duplicate_authorization"
	}
	expected := "Bearer " + s.token
	provided := values[0]
	if len(provided) != len(expected) {
		return "invalid_authorization"
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return "invalid_authorization"
	}
	return ""
}

func (s *webhookServer) rejectWebhook(w http.ResponseWriter, status int, reasonCode, message string) {
	s.logger.Warn("rejected webhook", "reason_code", reasonCode, "status_code", status)
	writeError(w, status, message)
}

func (s *webhookServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Health(r.Context()); err != nil {
		s.logger.Warn("database health check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *webhookServer) handleOperationalStatus(w http.ResponseWriter, r *http.Request) {
	s.writeOperationalStatus(w, r)
}

func (s *webhookServer) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if reasonCode := s.authorizationRejectionReason(r); reasonCode != "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="index01-status"`)
		s.logger.Warn("rejected readiness check", "reason_code", reasonCode, "status_code", http.StatusUnauthorized)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.writeOperationalStatus(w, r)
}

func (s *webhookServer) writeOperationalStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.OperationalStatus(r.Context())
	if err != nil {
		s.logger.Warn("operational status query failed")
		writeError(w, http.StatusServiceUnavailable, "operational status unavailable")
		return
	}
	httpStatus := http.StatusOK
	if status.Status != "ok" {
		httpStatus = http.StatusServiceUnavailable
	}
	writeJSON(w, httpStatus, status)
}

func parseWebhook(r *http.Request) (parsedWebhook, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return parsedWebhook{}, newWebhookRejectionError("multipart_reader", fmt.Errorf("create multipart reader: %w", err))
	}
	var payload parsedWebhook
	seen := make(map[string]bool)
	var recordedAt string
	partCount := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return parsedWebhook{}, newWebhookRejectionError("multipart_part", fmt.Errorf("read multipart part: %w", err))
		}
		partCount++
		if partCount > maxWebhookParts {
			_ = part.Close()
			return parsedWebhook{}, newWebhookRejectionError("part_count", fmt.Errorf("multipart part count exceeds %d", maxWebhookParts))
		}
		if err := validatePartHeaders(part); err != nil {
			_ = part.Close()
			return parsedWebhook{}, err
		}
		name := part.FormName()
		switch name {
		case "recordedAt", "client", "transcription":
			if seen[name] {
				_ = part.Close()
				return parsedWebhook{}, newWebhookRejectionError("duplicate_field", fmt.Errorf("duplicate %s field", name))
			}
			seen[name] = true
			limit := maxWebhookRecordedAtBytes
			switch name {
			case "client":
				limit = maxWebhookClientBytes
			case "transcription":
				limit = deepSeekMaxInputBytes
			}
			value, readErr := readWebhookField(part, limit)
			closeErr := part.Close()
			if readErr != nil {
				return parsedWebhook{}, newWebhookRejectionError("field_read", fmt.Errorf("read %s field: %w", name, readErr))
			}
			if closeErr != nil {
				return parsedWebhook{}, newWebhookRejectionError("part_close", fmt.Errorf("close %s field: %w", name, closeErr))
			}
			switch name {
			case "recordedAt":
				recordedAt = string(value)
			case "client":
				payload.client = strings.TrimSpace(string(value))
			case "transcription":
				payload.transcription = string(value)
			}
		case "audio":
			if seen[name] {
				_ = part.Close()
				return parsedWebhook{}, newWebhookRejectionError("duplicate_field", fmt.Errorf("duplicate audio field"))
			}
			seen[name] = true
			payload.audioPresent = true
			payload.audioFilename = part.FileName()
			if len(payload.audioFilename) > maxWebhookFilenameBytes || strings.ContainsAny(payload.audioFilename, "\r\n\x00") {
				_ = part.Close()
				return parsedWebhook{}, newWebhookRejectionError("audio_filename", fmt.Errorf("audio filename is invalid"))
			}
			digest := sha256.New()
			count, readErr := io.Copy(io.MultiWriter(io.Discard, digest), part)
			closeErr := part.Close()
			if readErr != nil {
				return parsedWebhook{}, newWebhookRejectionError("audio_read", fmt.Errorf("read audio field: %w", readErr))
			}
			if closeErr != nil {
				return parsedWebhook{}, newWebhookRejectionError("part_close", fmt.Errorf("close audio field: %w", closeErr))
			}
			payload.audioByteCount = count
			payload.audioDigest = hex.EncodeToString(digest.Sum(nil))
		default:
			_ = part.Close()
			return parsedWebhook{}, newWebhookRejectionError("unexpected_field", fmt.Errorf("unexpected multipart field"))
		}
	}
	if !seen["recordedAt"] {
		return parsedWebhook{}, newWebhookRejectionError("missing_recorded_at", fmt.Errorf("missing recordedAt field"))
	}
	millis, err := strconv.ParseInt(strings.TrimSpace(recordedAt), 10, 64)
	if err != nil || millis <= 0 {
		return parsedWebhook{}, newWebhookRejectionError("invalid_recorded_at", fmt.Errorf("recordedAt must be positive Unix milliseconds"))
	}
	payload.recordedAtMillis = millis
	if payload.client == "" {
		return parsedWebhook{}, newWebhookRejectionError("missing_client", fmt.Errorf("missing client field"))
	}
	if strings.ContainsAny(payload.client, "\r\n\x00") {
		return parsedWebhook{}, newWebhookRejectionError("invalid_client", fmt.Errorf("client field is invalid"))
	}
	payload.trigger = strings.TrimSpace(r.Header.Get("X-Index-Trigger"))
	if strings.ContainsAny(payload.trigger, "\r\n\x00") {
		return parsedWebhook{}, newWebhookRejectionError("invalid_trigger", fmt.Errorf("trigger header is invalid"))
	}
	return payload, nil
}

func validateWebhookHeaders(r *http.Request) error {
	headerCount := 0
	headerBytes := 0
	for name, values := range r.Header {
		headerCount += len(values)
		headerBytes += len(name)
		for _, value := range values {
			headerBytes += len(value)
		}
	}
	if headerCount > maxWebhookHeaders || headerBytes > maxWebhookHeaderBytes {
		return fmt.Errorf("request header limit exceeded")
	}
	if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Get("Content-Type")) > maxWebhookContentType {
		return fmt.Errorf("content type header is invalid")
	}
	triggerValues := r.Header.Values("X-Index-Trigger")
	if len(triggerValues) > 1 || len(r.Header.Get("X-Index-Trigger")) > maxWebhookTriggerBytes {
		return fmt.Errorf("trigger header is invalid")
	}
	return nil
}

func validatePartHeaders(part *multipart.Part) error {
	headerCount := 0
	headerBytes := 0
	for name, values := range part.Header {
		headerCount += len(values)
		headerBytes += len(name)
		for _, value := range values {
			headerBytes += len(value)
		}
	}
	if headerCount > maxWebhookPartHeaders || headerBytes > maxWebhookPartHeaderBytes {
		return newWebhookRejectionError("part_headers", fmt.Errorf("multipart part headers exceed limits"))
	}
	return nil
}

func readWebhookField(part io.Reader, limit int) ([]byte, error) {
	var value bytes.Buffer
	if _, err := io.CopyN(&value, part, int64(limit)+1); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if value.Len() > limit {
		return nil, errWebhookFieldTooLarge
	}
	return value.Bytes(), nil
}

func fingerprintWebhook(payload parsedWebhook) string {
	digest := sha256.New()
	writeFingerprintField(digest, strconv.FormatInt(payload.recordedAtMillis, 10))
	writeFingerprintField(digest, payload.client)
	writeFingerprintField(digest, payload.trigger)
	writeFingerprintField(digest, payload.transcription)
	if payload.audioPresent {
		writeFingerprintField(digest, "audio-present")
	} else {
		writeFingerprintField(digest, "audio-absent")
	}
	writeFingerprintField(digest, payload.audioFilename)
	writeFingerprintField(digest, strconv.FormatInt(payload.audioByteCount, 10))
	writeFingerprintField(digest, payload.audioDigest)
	return hex.EncodeToString(digest.Sum(nil))
}

func writeFingerprintField(digest hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = io.WriteString(digest, value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
