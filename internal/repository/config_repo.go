package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"workspace-tool/internal/domain"
	"workspace-tool/internal/infrastructure"
)

const (
	ConfigFilename = "config.json"
	ReposFilename  = "repos.json"

	CurrentConfigVersion = 1
	CurrentReposVersion  = 2
)

// ConfigWrapper encapsulates the config.json file schema with version.
type ConfigWrapper struct {
	Version      int                  `json:"version"`
	Config       domain.WorkspaceConfig `json:"config"`
	AuthProfiles []domain.AuthProfile `json:"auth_profiles"`
}

// ReposWrapper encapsulates the repos.json file schema with version.
type ReposWrapper struct {
	Version int           `json:"version"`
	Data    []domain.Repo `json:"data"`
}

// ConfigRepository manages persistent configurations and repositories in JSON files.
type ConfigRepository struct {
	mu            sync.RWMutex
	dirPath       string
	configCache   domain.WorkspaceConfig
	authCache     []domain.AuthProfile
	reposCache    []domain.Repo
	logger        *infrastructure.Logger
}

// NewConfigRepository creates a new ConfigRepository and ensures target directories exist.
func NewConfigRepository() (*ConfigRepository, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config dir: %w", err)
	}

	appDir := filepath.Join(userConfigDir, "workspace-tool")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create app config dir: %w", err)
	}

	repo := &ConfigRepository{
		dirPath: appDir,
		logger:  infrastructure.GetGlobalLogger(),
	}

	// Load or initialize configurations
	if err := repo.LoadAll(); err != nil {
		return nil, err
	}

	return repo, nil
}

// GetConfigPath returns the absolute path to config.json.
func (r *ConfigRepository) GetConfigPath() string {
	return filepath.Join(r.dirPath, ConfigFilename)
}

// GetReposPath returns the absolute path to repos.json.
func (r *ConfigRepository) GetReposPath() string {
	return filepath.Join(r.dirPath, ReposFilename)
}

// LoadAll loads both config.json and repos.json, running migrations if necessary.
func (r *ConfigRepository) LoadAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Load config.json
	configPath := r.GetConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Initialize default config
		r.logger.Info("Config file not found, initializing defaults at %s", configPath)
		r.configCache = domain.WorkspaceConfig{
			DefaultRootPath: filepath.Join(os.Getenv("USERPROFILE"), "Workspaces"),
			WorkerCount:     1,
		}
		r.authCache = []domain.AuthProfile{}
		if err := r.saveConfigLocked(); err != nil {
			return err
		}
	} else {
		// Run Config Migrations first
		if err := r.migrateConfigFile(configPath); err != nil {
			return fmt.Errorf("failed to migrate config file: %w", err)
		}

		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("failed to read config file: %w", err)
		}

		var wrapper ConfigWrapper
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse config file: %w", err)
		}
		r.configCache = wrapper.Config
		r.authCache = wrapper.AuthProfiles
	}

	// 2. Load repos.json
	reposPath := r.GetReposPath()
	if _, err := os.Stat(reposPath); os.IsNotExist(err) {
		r.logger.Info("Repos file not found, initializing empty database at %s", reposPath)
		r.reposCache = []domain.Repo{}
		if err := r.saveReposLocked(); err != nil {
			return err
		}
	} else {
		// Run Repos Migrations first
		if err := r.migrateReposFile(reposPath); err != nil {
			return fmt.Errorf("failed to migrate repos file: %w", err)
		}

		data, err := os.ReadFile(reposPath)
		if err != nil {
			return fmt.Errorf("failed to read repos file: %w", err)
		}

		var wrapper ReposWrapper
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return fmt.Errorf("failed to parse repos file: %w", err)
		}
		r.reposCache = wrapper.Data
	}

	return nil
}

// saveConfigLocked writes the configCache and authCache to config.json.
func (r *ConfigRepository) saveConfigLocked() error {
	wrapper := ConfigWrapper{
		Version:      CurrentConfigVersion,
		Config:       r.configCache,
		AuthProfiles: r.authCache,
	}

	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	configPath := r.GetConfigPath()
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// saveReposLocked writes the reposCache to repos.json.
func (r *ConfigRepository) saveReposLocked() error {
	wrapper := ReposWrapper{
		Version: CurrentReposVersion,
		Data:    r.reposCache,
	}

	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal repos: %w", err)
	}

	reposPath := r.GetReposPath()
	if err := os.WriteFile(reposPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write repos file: %w", err)
	}

	return nil
}

// GetConfig returns a copy of the WorkspaceConfig.
func (r *ConfigRepository) GetConfig() domain.WorkspaceConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.configCache
}

// SaveConfig updates the global WorkspaceConfig.
func (r *ConfigRepository) SaveConfig(cfg domain.WorkspaceConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configCache = cfg
	return r.saveConfigLocked()
}

// GetRepos returns a list of all Repos.
func (r *ConfigRepository) GetRepos() []domain.Repo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	// Return a deep copy to prevent external mutation
	repos := make([]domain.Repo, len(r.reposCache))
	copy(repos, r.reposCache)
	return repos
}

// SaveRepos saves a bulk list of Repos.
func (r *ConfigRepository) SaveRepos(repos []domain.Repo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reposCache = repos
	return r.saveReposLocked()
}

// AddRepo appends a single Repo, ensuring uniqueness of URL and Name.
func (r *ConfigRepository) AddRepo(repo domain.Repo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, existing := range r.reposCache {
		if existing.URL == repo.URL {
			return errors.New("repository with this URL already exists")
		}
		if existing.Name == repo.Name {
			return errors.New("repository with this Name already exists")
		}
	}

	r.reposCache = append(r.reposCache, repo)
	return r.saveReposLocked()
}

// UpdateRepo modifies an existing Repo in place.
func (r *ConfigRepository) UpdateRepo(repo domain.Repo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.reposCache {
		if existing.ID == repo.ID {
			// Validate that new name/url doesn't conflict with others
			for j, other := range r.reposCache {
				if i == j {
					continue
				}
				if other.Name == repo.Name {
					return errors.New("another repository with this Name already exists")
				}
				if other.URL == repo.URL {
					return errors.New("another repository with this URL already exists")
				}
			}
			r.reposCache[i] = repo
			return r.saveReposLocked()
		}
	}

	return errors.New("repository not found")
}

// DeleteRepo removes a Repo by ID.
func (r *ConfigRepository) DeleteRepo(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.reposCache {
		if existing.ID == id {
			r.reposCache = append(r.reposCache[:i], r.reposCache[i+1:]...)
			return r.saveReposLocked()
		}
	}

	return errors.New("repository not found")
}

// GetAuthProfiles returns a copy of all AuthProfiles.
func (r *ConfigRepository) GetAuthProfiles() []domain.AuthProfile {
	r.mu.RLock()
	defer r.mu.RUnlock()

	profiles := make([]domain.AuthProfile, len(r.authCache))
	copy(profiles, r.authCache)
	return profiles
}

// SaveAuthProfiles saves a list of AuthProfiles.
func (r *ConfigRepository) SaveAuthProfiles(profiles []domain.AuthProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authCache = profiles
	return r.saveConfigLocked()
}
