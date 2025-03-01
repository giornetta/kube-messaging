package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

type WebhookData struct {
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Body     string            `json:"body"`
	Metadata map[string]string `json:"metadata"`
}

func main() {
	log.Println("Starting webhook sender")

	// Get webhook data from environment variable
	webhookDataJSON := os.Getenv("WEBHOOK_DATA")
	if webhookDataJSON == "" {
		log.Fatal("WEBHOOK_DATA environment variable is required")
	}

	// Parse webhook data
	var webhookData WebhookData
	err := json.Unmarshal([]byte(webhookDataJSON), &webhookData)
	if err != nil {
		log.Fatalf("Failed to parse webhook data: %v", err)
	}

	// Log webhook data (for debugging)
	log.Printf("Sending webhook to: %s", webhookData.URL)
	log.Printf("Metadata: %v", webhookData.Metadata)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request
	req, err := http.NewRequest("POST", webhookData.URL, bytes.NewBufferString(webhookData.Body))
	if err != nil {
		log.Fatalf("Failed to create request: %v", err)
	}

	// Set headers
	for key, value := range webhookData.Headers {
		req.Header.Set(key, value)
	}

	// Set Content-Type if not already set
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	// Add metadata as headers for debugging
	for key, value := range webhookData.Metadata {
		req.Header.Set(fmt.Sprintf("X-Notification-%s", key), value)
	}

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Failed to send webhook: %v", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("Webhook sent successfully: %d %s", resp.StatusCode, resp.Status)
	} else {
		log.Fatalf("Webhook failed: %d %s", resp.StatusCode, resp.Status)
	}
}
