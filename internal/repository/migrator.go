package repository

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// MigrationFunc defines a function signature that takes raw JSON bytes, applies migrations, and returns updated JSON bytes.
type MigrationFunc func(rawData []byte) ([]byte, error)

// reposMigrations defines migrations for repos.json
var reposMigrations = map[int]MigrationFunc{
	// Migration 1 -> 2: Upgrades the schema of repos.json to include a "description" field with default value "".
	1: func(rawData []byte) ([]byte, error) {
		// Struct representing the old v1 schema (without description field)
		type OldRepoV1 struct {
			ID            string   `json:"id"`
			Name          string   `json:"name"`
			URL           string   `json:"url"`
			AuthProfileID string   `json:"auth_profile_id"`
			Tags          []string `json:"tags"`
		}
		var oldWrapper struct {
			Version int         `json:"version"`
			Data    []OldRepoV1 `json:"data"`
		}

		if err := json.Unmarshal(rawData, &oldWrapper); err != nil {
			return nil, fmt.Errorf("failed to parse v1 repos schema: %w", err)
		}

		// Struct representing the new v2 schema (with description field)
		type NewRepoV2 struct {
			ID            string   `json:"id"`
			Name          string   `json:"name"`
			URL           string   `json:"url"`
			AuthProfileID string   `json:"auth_profile_id"`
			Tags          []string `json:"tags"`
			Description   string   `json:"description"` // New field
		}

		newData := make([]NewRepoV2, 0, len(oldWrapper.Data))
		for _, old := range oldWrapper.Data {
			newData = append(newData, NewRepoV2{
				ID:            old.ID,
				Name:          old.Name,
				URL:           old.URL,
				AuthProfileID: old.AuthProfileID,
				Tags:          old.Tags,
				Description:   "", // Assign default value
			})
		}

		newWrapper := struct {
			Version int         `json:"version"`
			Data    []NewRepoV2 `json:"data"`
		}{
			Version: 2,
			Data:    newData,
		}

		bytes, err := json.MarshalIndent(newWrapper, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal upgraded repos schema: %w", err)
		}

		return bytes, nil
	},
}

// configMigrations defines migrations for config.json (currently none, but schema is ready)
var configMigrations = map[int]MigrationFunc{}

// migrateFile executes sequential migrations on a JSON file until it reaches the targetVersion.
func migrateFile(filePath string, targetVersion int, migrations map[int]MigrationFunc, backupMu *sync.RWMutex) error {
	// 1. Read existing raw file data
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file for migration: %w", err)
	}

	// 2. Parse only the version field
	var versionInspector struct {
		Version int `json:"version"`
	}

	// If the file is empty or contains invalid json, don't run migrations. Let load logic handle it.
	if err := json.Unmarshal(data, &versionInspector); err != nil {
		// If version is unreadable but json is there, treat version as 1
		var raw interface{}
		if json.Unmarshal(data, &raw) == nil {
			versionInspector.Version = 1
		} else {
			return nil // Let standard unmarshal fail with descriptive message
		}
	}

	currentVersion := versionInspector.Version
	if currentVersion == 0 {
		// Treat missing version as version 1
		currentVersion = 1
	}

	if currentVersion >= targetVersion {
		// No migration needed
		return nil
	}

	// 3. Create a backup file before modifying
	backupPath := filePath + ".bak"
	if err := copyFile(filePath, backupPath); err != nil {
		return fmt.Errorf("failed to create backup file %s: %w", backupPath, err)
	}

	// 4. Run migrations sequentially
	tempData := data
	for v := currentVersion; v < targetVersion; v++ {
		migFunc, ok := migrations[v]
		if !ok {
			return fmt.Errorf("missing migration path from version %d to %d", v, v+1)
		}

		updatedData, err := migFunc(tempData)
		if err != nil {
			return fmt.Errorf("migration from version %d to %d failed: %w", v, v+1, err)
		}
		tempData = updatedData
	}

	// 5. Write migrated content back
	if err := os.WriteFile(filePath, tempData, 0644); err != nil {
		return fmt.Errorf("failed to write migrated data back: %w", err)
	}

	return nil
}

// migrateConfigFile runs migrations for config.json.
func (r *ConfigRepository) migrateConfigFile(path string) error {
	return migrateFile(path, CurrentConfigVersion, configMigrations, &r.mu)
}

// migrateReposFile runs migrations for repos.json.
func (r *ConfigRepository) migrateReposFile(path string) error {
	r.logger.Info("Checking if database schema migration is required for %s", path)
	return migrateFile(path, CurrentReposVersion, reposMigrations, &r.mu)
}

// copyFile is a helper to duplicate a file securely.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
