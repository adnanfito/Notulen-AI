// Package stt talks to the local Python transcription service.
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ErrInvalidAudio means the service rejected the payload as undecodable.
// Retrying will not help, so the worker must not waste attempts on it.
var ErrInvalidAudio = errors.New("invalid audio")

type Result struct {
	Text                string  `json:"text"`
	Language            string  `json:"language"`
	LanguageProbability float64 `json:"language_probability"`
	DurationSeconds     float64 `json:"duration_seconds"`
	SegmentCount        int     `json:"segment_count"`
	ProcessingSeconds   float64 `json:"processing_seconds"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stt health returned %s", resp.Status)
	}
	return nil
}

// Transcribe streams an audio file to the STT service.
//
// The file is piped rather than buffered so a 90-second chunk never sits in
// backend RAM twice (PRD §39).
func (c *Client) Transcribe(ctx context.Context, path, language, initialPrompt string) (*Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open audio: %w", err)
	}
	defer file.Close()

	pr, pw := io.Pipe()
	form := multipart.NewWriter(pw)

	go func() {
		var writeErr error
		defer func() { _ = pw.CloseWithError(writeErr) }()

		part, err := form.CreateFormFile("file", filepath.Base(path))
		if err != nil {
			writeErr = err
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			writeErr = err
			return
		}
		if language != "" {
			_ = form.WriteField("language", language)
		}
		if initialPrompt != "" {
			_ = form.WriteField("initial_prompt", initialPrompt)
		}
		writeErr = form.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/transcribe", pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stt request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := parseError(body)

		// 4xx is a verdict about the payload, not a transient fault.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("%w: %s", ErrInvalidAudio, message)
		}
		return nil, fmt.Errorf("stt returned %s: %s", resp.Status, message)
	}

	var result Result
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode stt response: %w", err)
	}
	return &result, nil
}

// parseError pulls the message out of the STT error envelope, falling back to
// the raw body when it is not the shape we expect.
func parseError(body []byte) string {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return envelope.Error.Message
	}
	return string(bytes.TrimSpace(body))
}
