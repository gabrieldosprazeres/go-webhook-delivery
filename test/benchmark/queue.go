package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type queueStatus struct {
	Backlog   int `json:"backlog"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Total     int `json:"total"`
}

func waitQueueStatus(ctx context.Context, path string, expected int) (queueStatus, error) {
	if path == "" {
		return queueStatus{}, errors.New("database status path is required")
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			var status queueStatus
			if json.Unmarshal(raw, &status) != nil {
				return queueStatus{}, errors.New("invalid database status")
			}
			if status.Backlog != 0 || status.Succeeded != expected || status.Failed != 0 || status.Total != expected {
				return queueStatus{}, errors.New("database delivery set is incomplete")
			}
			return status, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return queueStatus{}, err
		}
		select {
		case <-ctx.Done():
			return queueStatus{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
