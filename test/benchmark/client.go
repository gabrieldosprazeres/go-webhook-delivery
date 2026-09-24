package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

const benchmarkEventType = "benchmark.delivery"

type benchClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type endpointResponse struct {
	SigningSecret struct {
		KeyID  string `json:"key_id"`
		Secret string `json:"secret"`
	} `json:"signing_secret"`
}

type loadResult struct {
	duration    time.Duration
	latencies   []time.Duration
	deliveryIDs []string
}

type eventResponse struct {
	DeliveryIDs []string `json:"delivery_ids"`
}

func newBenchClient(baseURL, credentialPath string) (*benchClient, error) {
	raw, err := os.ReadFile(credentialPath)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var credential struct {
		APIKey string `json:"api_key"`
	}
	if json.Unmarshal(raw, &credential) != nil || credential.APIKey == "" {
		return nil, errors.New("invalid credential file")
	}
	return &benchClient{baseURL: baseURL, apiKey: credential.APIKey,
		http: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (c *benchClient) createEndpoint(ctx context.Context, target string, receiver *receiver) error {
	body, _ := json.Marshal(map[string]any{"url": target, "event_types": []string{benchmarkEventType}})
	response, err := c.request(ctx, http.MethodPost, "/v1/endpoints", "", body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("endpoint creation returned %d", response.StatusCode)
	}
	var created endpointResponse
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&created) != nil {
		return errors.New("invalid endpoint response")
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(created.SigningSecret.Secret)
	if err != nil || len(secret) != 32 || created.SigningSecret.KeyID == "" {
		return errors.New("invalid signing material")
	}
	receiver.configure(created.SigningSecret.KeyID, secret)
	clear(secret)
	return nil
}

func (c *benchClient) ingest(ctx context.Context, count, concurrency int) (loadResult, error) {
	started := time.Now()
	jobs := make(chan int)
	latencies := make([]time.Duration, count)
	deliveryIDs := make([]string, count)
	errCh := make(chan error, concurrency)
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				begin := time.Now()
				body := []byte(fmt.Sprintf(`{"type":"%s","data":{"sequence":%d}}`, benchmarkEventType, index))
				response, err := c.request(ctx, http.MethodPost, "/v1/events", fmt.Sprintf("bench-%d", index), body)
				if err == nil {
					responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
					_ = response.Body.Close()
					if response.StatusCode != http.StatusAccepted {
						var problem struct {
							Code string `json:"code"`
						}
						_ = json.Unmarshal(responseBody, &problem)
						err = fmt.Errorf("ingestion returned %d (%s)", response.StatusCode, problem.Code)
					} else {
						var accepted eventResponse
						if json.Unmarshal(responseBody, &accepted) != nil || len(accepted.DeliveryIDs) != 1 ||
							accepted.DeliveryIDs[0] == "" {
							err = errors.New("invalid event delivery set")
						} else {
							deliveryIDs[index] = accepted.DeliveryIDs[0]
						}
					}
				}
				latencies[index] = time.Since(begin)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}()
	}
	for index := 0; index < count; index++ {
		select {
		case jobs <- index:
		case err := <-errCh:
			close(jobs)
			workers.Wait()
			return loadResult{}, err
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return loadResult{}, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	select {
	case err := <-errCh:
		return loadResult{}, err
	default:
	}
	seen := make(map[string]struct{}, count)
	for _, deliveryID := range deliveryIDs {
		if deliveryID == "" {
			return loadResult{}, errors.New("missing expected delivery ID")
		}
		if _, exists := seen[deliveryID]; exists {
			return loadResult{}, errors.New("duplicate expected delivery ID")
		}
		seen[deliveryID] = struct{}{}
	}
	return loadResult{duration: time.Since(started), latencies: latencies, deliveryIDs: deliveryIDs}, nil
}

func (c *benchClient) request(ctx context.Context, method, path, key string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	return c.http.Do(request)
}
