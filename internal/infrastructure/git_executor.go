package infrastructure

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"workspace-tool/internal/domain"
)

// Constants representing the Error Catalog
const (
	ErrGitNotFound     = "ERR_GIT_NOT_FOUND"
	ErrDirExists       = "ERR_DIR_EXISTS"
	ErrAuthRejected    = "ERR_AUTH_REJECTED"
	ErrNetworkTimeout  = "ERR_NETWORK_TIMEOUT"
	ErrCloneCancelled  = "ERR_CLONE_CANCELLED"
	ErrUnknownClone    = "ERR_UNKNOWN_CLONE"
)

// GitExecutor executes Git commands and handles error mapping and cleanup.
type GitExecutor struct {
	logger *Logger
}

// NewGitExecutor initializes a new GitExecutor.
func NewGitExecutor() *GitExecutor {
	return &GitExecutor{
		logger: GetGlobalLogger(),
	}
}

// CheckGitInstalled verifies if the git command is available in the system PATH.
func (ge *GitExecutor) CheckGitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Clone runs git clone sequentially, streaming progress and performing cleanup on error.
func (ge *GitExecutor) Clone(ctx context.Context, job domain.CloneJob, onProgress func(domain.CloneProgressEvent)) error {
	// 1. Verify Git Installation
	if !ge.CheckGitInstalled() {
		ge.logger.Error("Git is not found in system PATH.")
		return fmt.Errorf("%s: git executable not found", ErrGitNotFound)
	}

	// 2. Check Target Directory
	if _, err := os.Stat(job.TargetPath); err == nil {
		// Directory exists, check if empty
		files, err := os.ReadDir(job.TargetPath)
		if err == nil && len(files) > 0 {
			ge.logger.Warn("Target directory already exists and is not empty: %s", job.TargetPath)
			return fmt.Errorf("%s: target path '%s' is not empty", ErrDirExists, job.TargetPath)
		}
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(job.TargetPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directories: %w", err)
	}

	ge.logger.Info("Starting clone for %s into %s", job.Repo.Name, job.TargetPath)
	onProgress(domain.CreateProgressEvent("JOB_STARTED", job.Repo.ID, job.Repo.Name, domain.StateCloning, "Starting clone operation...", nil))

	// 3. Build Command
	cmd := exec.CommandContext(ctx, "git", "clone", job.Repo.URL, job.TargetPath)
	
	// Collect full output for error analysis
	var fullOutput strings.Builder
	
	// Setup ProgressStreamer
	streamer := &ProgressStreamer{
		RepoID:   job.Repo.ID,
		RepoName: job.Repo.Name,
		OnProgress: func(event domain.CloneProgressEvent) {
			fullOutput.WriteString(event.Payload.Message + "\n")
			onProgress(event)
		},
	}
	
	cmd.Stdout = streamer
	cmd.Stderr = streamer

	// 4. Run command
	err := cmd.Run()
	
	// Flush any remaining buffer in the streamer
	streamer.Flush()

	if err != nil {
		// 5. Cleanup Policy on Failure
		ge.logger.Warn("Clone failed for %s, starting cleanup...", job.Repo.Name)
		cleanupErr := os.RemoveAll(job.TargetPath)
		if cleanupErr != nil {
			ge.logger.Error("Cleanup failed to remove directory %s: %v", job.TargetPath, cleanupErr)
		} else {
			ge.logger.Info("Successfully cleaned up directory: %s", job.TargetPath)
		}

		// 6. Error Classification
		errStr := strings.ToLower(fullOutput.String())
		
		if ctx.Err() == context.Canceled {
			ge.logger.Warn("Clone operation cancelled by user/context for %s", job.Repo.Name)
			return fmt.Errorf("%s: operation cancelled", ErrCloneCancelled)
		}

		if strings.Contains(errStr, "authentication failed") || 
		   strings.Contains(errStr, "permission denied") || 
		   strings.Contains(errStr, "could not read from remote repository") {
			ge.logger.Error("Auth error cloning %s: credentials rejected", job.Repo.Name)
			return fmt.Errorf("%s: git authentication failed", ErrAuthRejected)
		}

		if strings.Contains(errStr, "could not resolve host") || 
		   strings.Contains(errStr, "network") || 
		   strings.Contains(errStr, "timeout") || 
		   strings.Contains(errStr, "connection refused") {
			ge.logger.Error("Network error cloning %s", job.Repo.Name)
			return fmt.Errorf("%s: git network or timeout failure", ErrNetworkTimeout)
		}

		ge.logger.Error("Unknown error cloning %s: %v", job.Repo.Name, err)
		return fmt.Errorf("%s: %v", ErrUnknownClone, err)
	}

	ge.logger.Info("Successfully cloned repo: %s", job.Repo.Name)
	return nil
}

// ProgressStreamer splits Git output by carriage returns or newlines to stream real-time events.
type ProgressStreamer struct {
	RepoID     string
	RepoName   string
	OnProgress func(domain.CloneProgressEvent)
	buffer     []byte
}

func (s *ProgressStreamer) Write(p []byte) (int, error) {
	s.buffer = append(s.buffer, p...)
	
	for {
		idx := -1
		for i, b := range s.buffer {
			if b == '\r' || b == '\n' {
				idx = i
				break
			}
		}
		if idx == -1 {
			break
		}
		
		lineBytes := s.buffer[:idx]
		s.buffer = s.buffer[idx+1:]
		
		line := strings.TrimSpace(string(lineBytes))
		if line != "" {
			s.sendLine(line)
		}
	}
	
	return len(p), nil
}

// Flush sends any remaining text in the buffer.
func (s *ProgressStreamer) Flush() {
	if len(s.buffer) > 0 {
		line := strings.TrimSpace(string(s.buffer))
		if line != "" {
			s.sendLine(line)
		}
		s.buffer = nil
	}
}

func (s *ProgressStreamer) sendLine(line string) {
	masked := MaskCredentials(line)
	event := domain.CreateProgressEvent("CLONE_PROGRESS", s.RepoID, s.RepoName, domain.StateCloning, masked, nil)
	s.OnProgress(event)
}
