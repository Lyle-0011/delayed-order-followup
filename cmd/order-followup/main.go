package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/example/delayed-order-followup/internal/followup"
	"github.com/example/delayed-order-followup/internal/infrai"
)

type service struct {
	queue        *infrai.Client
	actionURL    string
	actionClient *http.Client
}

func main() {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	actionURL := os.Getenv("ORDER_ACTION_URL")
	if actionURL == "" {
		log.Fatal("ORDER_ACTION_URL is required")
	}

	svc := &service{
		queue:        infrai.New(key),
		actionURL:    actionURL,
		actionClient: &http.Client{Timeout: 10 * time.Second},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /checkouts", svc.acceptCheckout)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	server := &http.Server{Addr: envOr("ADDR", ":8080"), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go svc.consume(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("order follow-up listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (s *service) acceptCheckout(w http.ResponseWriter, r *http.Request) {
	var checkout followup.Checkout
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&checkout); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid checkout"})
		return
	}
	if checkout.OrderID == "" || checkout.CustomerID == "" || checkout.Email == "" || checkout.DelayHours < 1 || checkout.DelayHours > 72 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "order_id, customer_id, email, and delay_hours from 1 to 72 are required"})
		return
	}
	action := followup.NewOrderAction(checkout, time.Now())
	if err := s.queue.Publish(r.Context(), action, "checkout-"+checkout.OrderID); err != nil {
		writeInfraiError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"order_id": action.OrderID, "due_at": action.DueAt})
}

func (s *service) consume(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.consumeBatch(ctx); err != nil && ctx.Err() == nil {
			log.Printf("consume: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *service) consumeBatch(ctx context.Context) error {
	messages, err := s.queue.Consume(ctx, 10, 30)
	if err != nil {
		return err
	}
	for _, message := range messages {
		var action followup.OrderAction
		if err := json.Unmarshal(message.Payload, &action); err != nil {
			return fmt.Errorf("decode message %s: %w", message.MessageID, err)
		}
		if !followup.Ready(action, time.Now()) {
			continue
		}
		if err := s.dispatch(ctx, action); err != nil {
			return err
		}
		if err := s.queue.Ack(ctx, message.MessageID); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) dispatch(ctx context.Context, action followup.OrderAction) error {
	events := []string{"fulfillment.requested", "receipt.send", "order.updated"}
	for _, event := range events {
		body, err := json.Marshal(map[string]any{"event": event, "order": action})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.actionURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", action.OrderID+":"+event)
		res, err := s.actionClient.Do(req)
		if err != nil {
			return fmt.Errorf("dispatch %s: %w", event, err)
		}
		res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("dispatch %s returned HTTP %d", event, res.StatusCode)
		}
	}
	return nil
}

func writeInfraiError(w http.ResponseWriter, err error) {
	var apiErr *infrai.Error
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		writeJSON(w, apiErr.HTTPStatus, map[string]string{"error": apiErr.Error()})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "queue request failed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
