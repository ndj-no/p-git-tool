package usecase

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"workspace-tool/internal/domain"
	"workspace-tool/internal/repository"

	"github.com/google/uuid"
)

// GitSyncManager coordinates API synchronization from GitHub and GitLab providers.
type GitSyncManager struct {
	configRepo *repository.ConfigRepository
	keyringRepo *repository.KeyringRepository
	client      *http.Client
}

// NewGitSyncManager initializes a new GitSyncManager.
func NewGitSyncManager(configRepo *repository.ConfigRepository, keyringRepo *repository.KeyringRepository) *GitSyncManager {
	return &GitSyncManager{
		configRepo:  configRepo,
		keyringRepo: keyringRepo,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// SyncProvider fetches all repositories for the given Auth Profile and upserts them into repos.json.
// Returns (addedCount, updatedCount, error)
func (g *GitSyncManager) SyncProvider(profileID string) (int, int, error) {
	// 1. Fetch Auth Profile
	profile, exists := g.configRepo.GetAuthProfileByID(profileID)
	if !exists {
		return 0, 0, fmt.Errorf("auth profile '%s' not found", profileID)
	}

	// 2. Fetch token from Secure OS Keyring
	token, err := g.keyringRepo.GetToken(profile.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to retrieve token from secure storage: %w", err)
	}

	var fetchedRepos []domain.Repo
	var fetchErr error

	// 3. Trigger provider-specific API fetch
	if strings.ToLower(profile.Provider) == "github" {
		fetchedRepos, fetchErr = g.fetchGitHub(token, profile.ID)
	} else if strings.ToLower(profile.Provider) == "gitlab" {
		fetchedRepos, fetchErr = g.fetchGitLab(token, profile.ID)
	} else {
		return 0, 0, fmt.Errorf("unsupported Git provider: %s", profile.Provider)
	}

	if fetchErr != nil {
		return 0, 0, fetchErr
	}

	// 4. Perform Upsert Merge Strategy
	localRepos := g.configRepo.GetRepos()
	added := 0
	updated := 0

	for _, fetched := range fetchedRepos {
		foundIdx := -1
		for idx, local := range localRepos {
			if strings.ToLower(local.URL) == strings.ToLower(fetched.URL) {
				foundIdx = idx
				break
			}
		}

		if foundIdx != -1 {
			// Update / Upsert: Update name and auth profile mapping, but PRESERVE local tags
			localRepos[foundIdx].Name = fetched.Name
			localRepos[foundIdx].AuthProfileID = fetched.AuthProfileID
			localRepos[foundIdx].Tags = repository.MergeTags(localRepos[foundIdx].Tags, fetched.Tags)
			updated++
		} else {
			// Insert new repository
			fetched.ID = uuid.New().String()
			localRepos = append(localRepos, fetched)
			added++
		}
	}

	// 5. Save back to config
	if err := g.configRepo.SaveRepos(localRepos); err != nil {
		return added, updated, fmt.Errorf("failed to save synced repositories: %w", err)
	}

	return added, updated, nil
}

// fetchGitHub queries GitHub API with full pagination.
func (g *GitSyncManager) fetchGitHub(token string, profileID string) ([]domain.Repo, error) {
	var repos []domain.Repo
	url := "https://api.github.com/user/repos?affiliation=owner,collaborator&per_page=100"

	nextLinkRegex := regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

	for url != "" {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github.v3+json")

		resp, err := g.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github API request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github API returned non-OK status: %s", resp.Status)
		}

		var ghRepos []struct {
			Name     string `json:"name"`
			CloneURL string `json:"clone_url"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&ghRepos); err != nil {
			return nil, fmt.Errorf("failed to decode GitHub response: %w", err)
		}

		for _, gr := range ghRepos {
			repos = append(repos, domain.Repo{
				Name:          gr.Name,
				URL:           gr.CloneURL,
				AuthProfileID: profileID,
				Tags:          []string{"github"},
			})
		}

		// Handle Link header pagination
		url = ""
		linkHeader := resp.Header.Get("Link")
		if linkHeader != "" {
			matches := nextLinkRegex.FindStringSubmatch(linkHeader)
			if len(matches) > 1 {
				url = matches[1]
			}
		}
	}

	return repos, nil
}

// fetchGitLab queries GitLab API with full pagination.
func (g *GitSyncManager) fetchGitLab(token string, profileID string) ([]domain.Repo, error) {
	var repos []domain.Repo
	baseURL := "https://gitlab.com/api/v4/projects?membership=true&per_page=100"
	page := "1"

	for page != "" {
		url := baseURL + "&page=" + page
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := g.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gitlab API request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gitlab API returned non-OK status: %s", resp.Status)
		}

		var glRepos []struct {
			Name             string   `json:"name"`
			HTTPURLToRepo    string   `json:"http_url_to_repo"`
			TagList          []string `json:"tag_list"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&glRepos); err != nil {
			return nil, fmt.Errorf("failed to decode GitLab response: %w", err)
		}

		for _, gr := range glRepos {
			tags := []string{"gitlab"}
			tags = append(tags, gr.TagList...)

			repos = append(repos, domain.Repo{
				Name:          gr.Name,
				URL:           gr.HTTPURLToRepo,
				AuthProfileID: profileID,
				Tags:          tags,
			})
		}

		// Handle pagination via X-Next-Page
		page = resp.Header.Get("X-Next-Page")
	}

	return repos, nil
}
