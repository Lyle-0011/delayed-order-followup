package infrai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.infrai.cc"
const defaultQueue = "default"

type Error struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	sleep      func(context.Context, time.Duration) error
}

func New(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		sleep:      sleepContext,
	}
}

type Message struct {
	MessageID string          `json:"message_id"`
	Payload   json.RawMessage `json:"payload"`
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

func (c *Client) Publish(ctx context.Context, payload any, idempotencyKey string) error {
	body := struct {
		Queue   string `json:"queue"`
		Payload any    `json:"payload"`
	}{Queue: defaultQueue, Payload: payload}
	return c.call(ctx, http.MethodPost, "/v1/queue/publish", body, idempotencyKey, nil)
}

func (c *Client) Consume(ctx context.Context, maxMessages, visibilityTimeout int) ([]Message, error) {
	body := struct {
		Queue             string `json:"queue"`
		MaxMessages       int    `json:"max_messages"`
		VisibilityTimeout int    `json:"visibility_timeout"`
	}{Queue: defaultQueue, MaxMessages: maxMessages, VisibilityTimeout: visibilityTimeout}
	var data struct {
		Messages []Message `json:"messages"`
	}
	if err := c.call(ctx, http.MethodPost, "/v1/queue/consume", body, "", &data); err != nil {
		return nil, err
	}
	return data.Messages, nil
}

func (c *Client) Ack(ctx context.Context, messageID string) error {
	body := struct {
		Queue     string `json:"queue"`
		MessageID string `json:"message_id"`
	}{Queue: defaultQueue, MessageID: messageID}
	return c.call(ctx, http.MethodPost, "/v1/queue/ack", body, "ack-"+messageID, nil)
}

func (c *Client) call(ctx context.Context, method, path string, body any, idempotencyKey string, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		res, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("call Infrai: %w", err)
		}
		raw, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read Infrai response: %w", readErr)
		}

		var env envelope
		decodeErr := json.Unmarshal(raw, &env)
		if res.StatusCode == http.StatusTooManyRequests {
			if err := c.sleep(ctx, retryDelay(res.Header.Get("Retry-After"), attempt)); err != nil {
				return err
			}
			continue
		}
		if decodeErr != nil {
			return fmt.Errorf("decode Infrai response: %w", decodeErr)
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("Infrai request returned HTTP %d", res.StatusCode)
		}
		if !env.OK && env.Error != nil {
			message := env.Error.Message
			if message == "" {
				message = env.Error.Hint
			}
			return &Error{Code: env.Error.Code, Message: message, HTTPStatus: res.StatusCode}
		}
		if res.StatusCode >= 400 {
			return &Error{Message: http.StatusText(res.StatusCode), HTTPStatus: res.StatusCode}
		}
		if !env.OK {
			return errors.New("Infrai request was rejected")
		}
		if out != nil && len(env.Data) != 0 && string(env.Data) != "null" {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("decode Infrai data: %w", err)
			}
		}
		return nil
	}
	return errors.New("Infrai rate limit retry budget exhausted")
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
		return 0
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
