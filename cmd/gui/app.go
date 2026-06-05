package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"workspace-tool/internal/domain"
	"workspace-tool/internal/infrastructure"
	"workspace-tool/internal/repository"
	"workspace-tool/internal/usecase"

	"github.com/google/uuid"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct manages Go-JS bindings for Wails.
type App struct {
	ctx          context.Context
	configRepo   *repository.ConfigRepository
	keyringRepo  *repository.KeyringRepository
	gitExecutor  *infrastructure.GitExecutor
	syncManager  *usecase.GitSyncManager
	pipeline     *usecase.ClonePipeline
	decisionChan chan usecase.FailureAction
}

// NewApp creates and initializes all repository and usecase instances for the Wails app.
func NewApp() *App {
	cfgRepo, err := repository.NewConfigRepository()
	if err != nil {
		fmt.Printf("Error initializing config repository: %v\n", err)
	}

	keyring := repository.NewKeyringRepository()
	executor := infrastructure.NewGitExecutor()
	sync := usecase.NewGitSyncManager(cfgRepo, keyring)

	return &App{
		configRepo:   cfgRepo,
		keyringRepo:  keyring,
		gitExecutor:  executor,
		syncManager:  sync,
		decisionChan: make(chan usecase.FailureAction, 1),
	}
}

// startup is called when Wails initializes.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// --------------------------------------------------------------------
// 1. Workspace Configuration
// --------------------------------------------------------------------

func (a *App) GetConfig() domain.WorkspaceConfig {
	return a.configRepo.GetConfig()
}

func (a *App) SaveConfig(defaultRootPath string, workerCount int) string {
	cfg := domain.WorkspaceConfig{
		DefaultRootPath: defaultRootPath,
		WorkerCount:     workerCount,
	}
	if err := a.configRepo.SaveConfig(cfg); err != nil {
		return err.Error()
	}
	return ""
}

// --------------------------------------------------------------------
// 2. Repository Management
// --------------------------------------------------------------------

func (a *App) GetRepos() []domain.Repo {
	return a.configRepo.GetRepos()
}

func (a *App) AddRepo(name, url, authProfileID, tagsStr string) string {
	tags := repository.ParseTagsCell(tagsStr)
	repo := domain.Repo{
		ID:            uuid.New().String(),
		Name:          name,
		URL:           url,
		AuthProfileID: authProfileID,
		Tags:          tags,
	}

	if err := a.configRepo.AddRepo(repo); err != nil {
		return err.Error()
	}
	return ""
}

func (a *App) UpdateRepo(id, name, url, authProfileID, tagsStr string) string {
	tags := repository.ParseTagsCell(tagsStr)
	repo := domain.Repo{
		ID:            id,
		Name:          name,
		URL:           url,
		AuthProfileID: authProfileID,
		Tags:          tags,
	}

	if err := a.configRepo.UpdateRepo(repo); err != nil {
		return err.Error()
	}
	return ""
}

func (a *App) DeleteRepo(id string) string {
	if err := a.configRepo.DeleteRepo(id); err != nil {
		return err.Error()
	}
	return ""
}

func (a *App) ImportCSV() string {
	filePath, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Repositories CSV File",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "CSV Files (*.csv)",
				Pattern:     "*.csv",
			},
		},
	})
	if err != nil {
		return fmt.Sprintf("Error selecting file: %s", err.Error())
	}
	if filePath == "" {
		return "" // User cancelled
	}

	inserted, updated, err := a.configRepo.ImportReposFromCSV(filePath)
	if err != nil {
		return fmt.Sprintf("Error: %s", err.Error())
	}
	return fmt.Sprintf("Imported successfully! Added %d, Upserted %d.", inserted, updated)
}

func (a *App) ExportCSVTemplate() string {
	filePath, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: "repos_template.csv",
		Title:           "Save CSV Template",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "CSV Files (*.csv)",
				Pattern:     "*.csv",
			},
		},
	})
	if err != nil {
		return fmt.Sprintf("Error showing save dialog: %s", err.Error())
	}
	if filePath == "" {
		return "" // User cancelled
	}

	err = a.configRepo.ExportCSVTemplate(filePath)
	if err != nil {
		return fmt.Sprintf("Error: %s", err.Error())
	}
	return "Template CSV file exported successfully!"
}

// --------------------------------------------------------------------
// 3. Authentication Management
// --------------------------------------------------------------------

func (a *App) GetAuthProfiles() []domain.AuthProfile {
	return a.configRepo.GetAuthProfiles()
}

func (a *App) AddAuthProfile(id, provider, username, token string, isDefault bool) string {
	profile := domain.AuthProfile{
		ID:        id,
		Name:      provider,
		Provider:  strings.ToLower(provider),
		Username:  username,
		IsDefault: isDefault,
	}

	// 1. Secure token in OS Keyring
	if err := a.keyringRepo.SaveToken(profile.ID, token); err != nil {
		return fmt.Sprintf("Secure Storage Error: %s", err.Error())
	}

	// 2. Save metadata in config
	if err := a.configRepo.AddAuthProfile(profile); err != nil {
		_ = a.keyringRepo.DeleteToken(profile.ID) // Rollback
		return err.Error()
	}

	return ""
}

func (a *App) DeleteAuthProfile(id string) string {
	_ = a.keyringRepo.DeleteToken(id)
	if err := a.configRepo.DeleteAuthProfile(id); err != nil {
		return err.Error()
	}
	return ""
}

// --------------------------------------------------------------------
// 4. Git API Synchronization
// --------------------------------------------------------------------

func (a *App) SyncProvider(profileID string) string {
	added, updated, err := a.syncManager.SyncProvider(profileID)
	if err != nil {
		return fmt.Sprintf("Error: %s", err.Error())
	}
	return fmt.Sprintf("Sync complete! Added %d, Updated %d repositories.", added, updated)
}

// --------------------------------------------------------------------
// 5. Asynchronous Clone Execution Pipeline
// --------------------------------------------------------------------

func (a *App) StartClone(taskName, targetPath string, repoIDs []string) string {
	if !a.gitExecutor.CheckGitInstalled() {
		return "Git is not installed or not in PATH."
	}

	allRepos := a.configRepo.GetRepos()
	repoMap := make(map[string]domain.Repo)
	for _, r := range allRepos {
		repoMap[r.ID] = r
	}

	// Clean active pipeline
	a.pipeline = usecase.NewClonePipeline(a.gitExecutor)

	// Register block channel failure callback
	a.pipeline.RegisterFailureHandler(func(job domain.CloneJob, err error) usecase.FailureAction {
		// Emit error prompt event to frontend
		wailsRuntime.EventsEmit(a.ctx, "clone_error_prompt", map[string]interface{}{
			"repo_name": job.Repo.Name,
			"error":     err.Error(),
		})

		// Drain channel first to make it safe
		for len(a.decisionChan) > 0 {
			<-a.decisionChan
		}

		// Block waiting for user response from frontend via SendFailureResponse method
		action := <-a.decisionChan
		return action
	})

	// Add selected repositories
	for _, id := range repoIDs {
		r, exists := repoMap[id]
		if !exists {
			continue
		}

		serviceDir := filepath.Join(targetPath, r.Name)

		// Inject credentials for HTTPS/HTTP
		if strings.HasPrefix(r.URL, "http://") || strings.HasPrefix(r.URL, "https://") {
			profileID := r.AuthProfileID
			if profileID == "" {
				if defProfile, hasDef := a.configRepo.GetDefaultAuthProfile(); hasDef {
					profileID = defProfile.ID
				}
			}

			if profileID != "" {
				token, err := a.keyringRepo.GetToken(profileID)
				if err == nil {
					profile, _ := a.configRepo.GetAuthProfileByID(profileID)
					username := profile.Username
					if username == "" {
						username = "git"
					}

					var authenticatedURL string
					if strings.HasPrefix(r.URL, "https://") {
						urlWithoutProto := strings.TrimPrefix(r.URL, "https://")
						authenticatedURL = fmt.Sprintf("https://%s:%s@%s", username, token, urlWithoutProto)
					} else {
						urlWithoutProto := strings.TrimPrefix(r.URL, "http://")
						authenticatedURL = fmt.Sprintf("http://%s:%s@%s", username, token, urlWithoutProto)
					}
					r.URL = authenticatedURL
				}
			}
		}

		a.pipeline.AddJob(r, serviceDir)
	}

	// Start asynchronous execution goroutine
	go func() {
		eventChan := a.pipeline.Start(context.Background())
		for event := range eventChan {
			// Broadcast events in real-time to frontend Vue/Svelte/React/Vanilla listener
			wailsRuntime.EventsEmit(a.ctx, "clone_event", event)
		}
	}()

	return ""
}

func (a *App) CancelClone() {
	if a.pipeline != nil {
		a.pipeline.Cancel()
	}
}

func (a *App) SendFailureResponse(action string) {
	decision := usecase.ActionSkip
	if strings.ToLower(action) == "retry" {
		decision = usecase.ActionRetry
	}

	select {
	case a.decisionChan <- decision:
		// Sent decision successfully
	default:
		// Channel already full, do nothing
	}
}
