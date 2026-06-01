package repository

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"workspace-tool/internal/domain"

	"github.com/google/uuid"
)

// IsValidGitURL validates if a string is a standard HTTPS, HTTP, or SSH Git clone URL.
func IsValidGitURL(url string) bool {
	url = strings.TrimSpace(url)
	if url == "" {
		return false
	}
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		// Needs to contain host and path
		return strings.Count(url, "/") >= 3
	}
	if strings.HasPrefix(url, "git@") {
		// git@domain:path
		return strings.Contains(url, ":") && strings.Contains(url, "@")
	}
	if strings.HasPrefix(url, "ssh://") {
		// ssh://git@domain:port/path or ssh://git@domain/path
		return strings.Count(url, "/") >= 3
	}
	return false
}

// ParseTagsCell converts a tag string (separated by semicolon or comma) into a slice of trimmed strings.
func ParseTagsCell(tagsStr string) []string {
	tagsStr = strings.TrimSpace(tagsStr)
	if tagsStr == "" {
		return []string{}
	}

	var rawTokens []string
	if strings.Contains(tagsStr, ";") {
		rawTokens = strings.Split(tagsStr, ";")
	} else {
		rawTokens = strings.Split(tagsStr, ",")
	}

	var tags []string
	seen := make(map[string]bool)
	for _, token := range rawTokens {
		token = strings.TrimSpace(token)
		if token != "" {
			normalized := strings.ToLower(token)
			if !seen[normalized] {
				seen[normalized] = true
				tags = append(tags, token) // Preserve original casing or format, search is case-insensitive
			}
		}
	}
	return tags
}

// MergeTags combines existing local tags with new tags, ensuring uniqueness and preserving custom local tags.
func MergeTags(existing []string, imported []string) []string {
	seen := make(map[string]bool)
	var merged []string

	// Load existing tags first
	for _, t := range existing {
		t = strings.TrimSpace(t)
		if t != "" {
			normalized := strings.ToLower(t)
			if !seen[normalized] {
				seen[normalized] = true
				merged = append(merged, t)
			}
		}
	}

	// Add imported tags if not already present
	for _, t := range imported {
		t = strings.TrimSpace(t)
		if t != "" {
			normalized := strings.ToLower(t)
			if !seen[normalized] {
				seen[normalized] = true
				merged = append(merged, t)
			}
		}
	}

	return merged
}

// ImportReposFromCSV parses a CSV file and upserts repositories in repos.json.
// Returns: (insertedCount, updatedCount, error)
func (r *ConfigRepository) ImportReposFromCSV(filePath string) (int, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Read header row
	header, err := reader.Read()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read CSV header: %w", err)
	}

	// Map headers to column indices
	colIndices := map[string]int{
		"name":         -1,
		"url":          -1,
		"tags":         -1,
		"auth_profile": -1,
	}

	for i, col := range header {
		col = strings.TrimSpace(strings.ToLower(col))
		if _, exists := colIndices[col]; exists {
			colIndices[col] = i
		}
	}

	// Verify required headers
	if colIndices["name"] == -1 || colIndices["url"] == -1 {
		return 0, 0, errors.New("CSV file must contain 'name' and 'url' columns")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	inserted := 0
	updated := 0
	lineNum := 1 // Header is line 1

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		lineNum++
		if err != nil {
			r.logger.Warn("Row %d: skipped due to CSV parsing error: %v", lineNum, err)
			continue
		}

		// Parse name
		name := ""
		if colIndices["name"] < len(record) {
			name = strings.TrimSpace(record[colIndices["name"]])
		}

		// Parse url
		url := ""
		if colIndices["url"] < len(record) {
			url = strings.TrimSpace(record[colIndices["url"]])
		}

		// Validation checks
		if name == "" || url == "" {
			r.logger.Warn("Row %d: skipped because 'name' or 'url' is empty", lineNum)
			continue
		}

		if !IsValidGitURL(url) {
			r.logger.Warn("Row %d: skipped because Git URL '%s' is invalid", lineNum, url)
			continue
		}

		// Parse tags
		tagsStr := ""
		if colIndices["tags"] != -1 && colIndices["tags"] < len(record) {
			tagsStr = record[colIndices["tags"]]
		}
		newTags := ParseTagsCell(tagsStr)

		// Parse auth profile
		authProfileID := ""
		if colIndices["auth_profile"] != -1 && colIndices["auth_profile"] < len(record) {
			authProfileID = strings.TrimSpace(record[colIndices["auth_profile"]])
		}

		// Search for duplicates by URL in cache
		foundIdx := -1
		for i, existing := range r.reposCache {
			if existing.URL == url {
				foundIdx = i
				break
			}
		}

		if foundIdx != -1 {
			// Update / Upsert: Update name, merge tags, update auth profile, keep description if exists
			existing := r.reposCache[foundIdx]
			
			// Validate if another repo has the same name
			nameConflict := false
			for i, other := range r.reposCache {
				if i != foundIdx && other.Name == name {
					nameConflict = true
					break
				}
			}
			if nameConflict {
				r.logger.Warn("Row %d: skipped because name '%s' conflicts with another repository", lineNum, name)
				continue
			}

			existing.Name = name
			existing.Tags = MergeTags(existing.Tags, newTags)
			if authProfileID != "" {
				existing.AuthProfileID = authProfileID
			}

			r.reposCache[foundIdx] = existing
			updated++
		} else {
			// Insert new record
			// Validate if name already exists
			nameConflict := false
			for _, other := range r.reposCache {
				if other.Name == name {
					nameConflict = true
					break
				}
			}
			if nameConflict {
				r.logger.Warn("Row %d: skipped because name '%s' already exists in databases", lineNum, name)
				continue
			}

			newRepo := domain.Repo{
				ID:            uuid.New().String(),
				Name:          name,
				URL:           url,
				AuthProfileID: authProfileID,
				Tags:          newTags,
				Description:   "", // Default empty
			}

			r.reposCache = append(r.reposCache, newRepo)
			inserted++
		}
	}

	// Save to JSON
	if err := r.saveReposLocked(); err != nil {
		return inserted, updated, fmt.Errorf("failed to save imported repos: %w", err)
	}

	return inserted, updated, nil
}
