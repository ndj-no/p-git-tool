package domain

import (
	"time"
)

// CloneJobState represents the current state of a repo clone job.
type CloneJobState string

const (
	StatePending   CloneJobState = "PENDING"
	StateCloning   CloneJobState = "CLONING"
	StateSuccess   CloneJobState = "SUCCESS"
	StateFailed    CloneJobState = "FAILED"
	StateCancelled CloneJobState = "CANCELLED"
)

// WorkspaceConfig holds the global workspace configurations.
type WorkspaceConfig struct {
	DefaultRootPath string `json:"default_root_path"`
	WorkerCount     int    `json:"worker_count"`
}

// Repo represents a microservice repository.
type Repo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	URL           string   `json:"url"`
	AuthProfileID string   `json:"auth_profile_id"`
	Tags          []string `json:"tags"`
	Description   string   `json:"description"`
}

// AuthProfile holds user credential metadata. Token is stored securely in OS Keyring.
type AuthProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"` // "github" or "gitlab"
	Username  string `json:"username"`
	IsDefault bool   `json:"is_default"`
}

// CloneJob defines a single microservice cloning job in the execution pipeline.
type CloneJob struct {
	ID           string        `json:"id"`
	Repo         Repo          `json:"repo"`
	State        CloneJobState `json:"state"`
	TargetPath   string        `json:"target_path"`
	ErrorMessage string        `json:"error_message"`
}

// CloneProgressPayload represents the payload data of a clone event.
type CloneProgressPayload struct {
	RepoID    string  `json:"repo_id"`
	RepoName  string  `json:"repo_name"`
	State     string  `json:"state"`
	Message   string  `json:"message"`
	ErrorCode *string `json:"error_code"`
}

// CloneProgressEvent is the event contract used for core to UI communication.
type CloneProgressEvent struct {
	EventType string               `json:"event_type"` // JOB_STARTED, CLONE_PROGRESS, JOB_COMPLETED, JOB_FAILED
	Payload   CloneProgressPayload `json:"payload"`
	Timestamp string               `json:"timestamp"`
}

// CreateProgressEvent helper builds a standardized progress event.
func CreateProgressEvent(eventType string, repoID string, repoName string, state CloneJobState, message string, errorCode *string) CloneProgressEvent {
	return CloneProgressEvent{
		EventType: eventType,
		Payload: CloneProgressPayload{
			RepoID:    repoID,
			RepoName:  repoName,
			State:     string(state),
			Message:   message,
			ErrorCode: errorCode,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}
