package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"likha/internal/model"
	"likha/internal/providers"
)

const modelMetadataLogName = "model-metadata.log"

type modelMetadataLog struct {
	file   *os.File
	logger *log.Logger
	path   string
}

func openModelMetadataLog(stateDir string) (*modelMetadataLog, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	path := filepath.Join(stateDir, modelMetadataLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open model metadata log: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, fmt.Errorf("protect model metadata log: %w", err)
	}
	l := &modelMetadataLog{file: file, logger: log.New(file, "", log.LstdFlags|log.Lmicroseconds), path: path}
	l.logger.Print("model metadata diagnostics enabled; API keys, request headers, and prompts are not logged")
	return l, nil
}

func (l *modelMetadataLog) close() {
	if l != nil && l.file != nil {
		_ = l.file.Close()
	}
}

func (l *modelMetadataLog) observe(provider, operation string, elapsed time.Duration, details []model.ModelDetails, err error) {
	if l == nil || l.logger == nil {
		return
	}
	if err != nil {
		l.logger.Printf("model-list provider=%q operation=%q elapsed=%s error=%q", provider, operation, elapsed.Round(time.Millisecond), err.Error())
		return
	}
	l.logger.Printf("model-list provider=%q operation=%q elapsed=%s models=%d", provider, operation, elapsed.Round(time.Millisecond), len(details))
	for _, detail := range details {
		if detail.ContextWindow > 0 {
			l.logger.Printf("model provider=%q id=%q context_window=%d field=%q", provider, detail.ID, detail.ContextWindow, detail.ContextWindowSource)
			continue
		}
		l.logger.Printf("model provider=%q id=%q context_window=unknown", provider, detail.ID)
	}
}

func (l *modelMetadataLog) observer() providers.ModelMetadataObserver {
	if l == nil {
		return nil
	}
	return l.observe
}
