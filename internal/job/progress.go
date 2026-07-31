package job

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidProgress = errors.New("invalid job progress")

// ProgressPhase names the user-visible activity currently moving bytes.
type ProgressPhase string

const (
	ProgressSnapshot ProgressPhase = "snapshot"
	ProgressUpload   ProgressPhase = "upload"
	ProgressDownload ProgressPhase = "download"
	ProgressRestore  ProgressPhase = "restore"
	ProgressCollect  ProgressPhase = "collect"
	ProgressPull     ProgressPhase = "pull"
)

var validProgressPhases = map[ProgressPhase]struct{}{
	ProgressSnapshot: {},
	ProgressUpload:   {},
	ProgressDownload: {},
	ProgressRestore:  {},
	ProgressCollect:  {},
	ProgressPull:     {},
}

// Progress is the latest durable byte-level progress for one job operation.
type Progress struct {
	Phase          ProgressPhase
	CompletedBytes int64
	TotalBytes     int64
	UpdatedAt      time.Time
}

func ParseProgressPhase(value string) (ProgressPhase, error) {
	phase := ProgressPhase(value)
	if !phase.Valid() {
		return "", fmt.Errorf("%w: phase %q", ErrInvalidProgress, value)
	}
	return phase, nil
}

func (phase ProgressPhase) Valid() bool {
	_, ok := validProgressPhases[phase]
	return ok
}

func (progress Progress) Validate() error {
	if !progress.Phase.Valid() {
		return fmt.Errorf("%w: phase %q", ErrInvalidProgress, progress.Phase)
	}
	if progress.TotalBytes <= 0 {
		return fmt.Errorf("%w: total bytes must be positive", ErrInvalidProgress)
	}
	if progress.CompletedBytes < 0 || progress.CompletedBytes > progress.TotalBytes {
		return fmt.Errorf("%w: completed bytes out of range", ErrInvalidProgress)
	}
	if progress.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: update time is required", ErrInvalidProgress)
	}
	return nil
}

func cloneProgress(progress *Progress) *Progress {
	if progress == nil {
		return nil
	}
	clone := *progress
	clone.UpdatedAt = clone.UpdatedAt.UTC()
	return &clone
}
