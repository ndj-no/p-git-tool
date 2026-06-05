package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"workspace-tool/internal/domain"
	"workspace-tool/internal/infrastructure"
	"workspace-tool/internal/repository"
	"workspace-tool/internal/usecase"

	"github.com/AlecAivazis/survey/v2"
	"github.com/google/uuid"
)

var (
	configRepo  *repository.ConfigRepository
	keyringRepo *repository.KeyringRepository
	gitExecutor *infrastructure.GitExecutor
	syncManager *usecase.GitSyncManager
	logger      *infrastructure.Logger
)

func main() {
	var err error
	// 1. Initialize Logger
	logger, err = infrastructure.NewLogger(infrastructure.LevelInfo)
	if err != nil {
		fmt.Printf("Error initializing logger: %v\n", err)
		os.Exit(1)
	}

	// 2. Initialize Repositories
	configRepo, err = repository.NewConfigRepository()
	if err != nil {
		logger.Error("Failed to initialize config repository: %v", err)
		os.Exit(1)
	}

	keyringRepo = repository.NewKeyringRepository()
	gitExecutor = infrastructure.NewGitExecutor()
	syncManager = usecase.NewGitSyncManager(configRepo, keyringRepo)

	logger.Info("Workspace Automation Tool initialized successfully.")

	// 3. Main Loop
	for {
		clearScreen()
		fmt.Println("=========================================================")
		fmt.Println("      WORKSPACE & MICROSERVICES AUTOMATION TOOL (v3.0)   ")
		fmt.Println("=========================================================")

		options := []string{
			"1. Setup New Task Workspace (Khởi tạo Workspace mới)",
			"2. Manage Repositories (Quản lý Repositories)",
			"3. Manage Authentication Profiles (Quản lý Xác thực)",
			"4. Sync from Git Providers (Đồng bộ qua API)",
			"5. View Settings (Cấu hình hệ thống)",
			"6. Exit (Thoát)",
		}

		var choice string
		prompt := &survey.Select{
			Message: "Select an action:",
			Options: options,
		}

		err := survey.AskOne(prompt, &choice)
		if err != nil {
			fmt.Println("Exiting due to input error:", err)
			break
		}

		if choice == options[0] {
			setupWorkspaceFlow()
		} else if choice == options[1] {
			manageReposFlow()
		} else if choice == options[2] {
			manageAuthFlow()
		} else if choice == options[3] {
			syncGitFlow()
		} else if choice == options[4] {
			viewSettingsFlow()
		} else if choice == options[5] {
			fmt.Println("\nThank you for using Workspace Automation Tool. Goodbye!")
			break
		}

		// Wait for user before looping back
		fmt.Print("\n[Press Enter to return to Main Menu]")
		fmt.Scanln()
	}
}

// --------------------------------------------------------------------
// Flows & Sub-flows
// --------------------------------------------------------------------

func setupWorkspaceFlow() {
	fmt.Println("\n--- SETUP NEW TASK WORKSPACE ---")
	
	// Check if git is installed
	if !gitExecutor.CheckGitInstalled() {
		fmt.Println("\n[ERROR] Git is not installed or not in the PATH. Cannot clone.")
		return
	}

	// 1. Prompt Task ID/Name
	var taskName string
	err := survey.AskOne(&survey.Input{
		Message: "Enter Task Name/ID (e.g., TASK-1024):",
	}, &taskName, survey.WithValidator(survey.Required))
	if err != nil {
		return
	}
	taskName = strings.TrimSpace(taskName)

	// 2. Propose Target Path with override
	defaultRoot := configRepo.GetConfig().DefaultRootPath
	proposedPath := filepath.Join(defaultRoot, taskName)

	var targetPath string
	err = survey.AskOne(&survey.Input{
		Message: "Target path (Press Enter to confirm or type to override):",
		Default: proposedPath,
	}, &targetPath, survey.WithValidator(survey.Required))
	if err != nil {
		return
	}
	targetPath = strings.TrimSpace(targetPath)

	// 3. Ask if they want to filter by tag
	var filterTags bool
	err = survey.AskOne(&survey.Confirm{
		Message: "Filter repositories by tag?",
		Default: false,
	}, &filterTags)
	if err != nil {
		return
	}

	allRepos := configRepo.GetRepos()
	var reposToSelect []domain.Repo

	if filterTags {
		var tagQuery string
		err = survey.AskOne(&survey.Input{
			Message: "Enter tag to filter (e.g. backend, go):",
		}, &tagQuery)
		if err != nil {
			return
		}
		tagQuery = strings.TrimSpace(strings.ToLower(tagQuery))

		for _, r := range allRepos {
			matches := false
			for _, t := range r.Tags {
				if strings.Contains(strings.ToLower(t), tagQuery) {
					matches = true
					break
				}
			}
			if matches {
				reposToSelect = append(reposToSelect, r)
			}
		}

		if len(reposToSelect) == 0 {
			fmt.Printf("\n[WARN] No repositories matched the tag: '%s'\n", tagQuery)
			return
		}
	} else {
		reposToSelect = allRepos
	}

	if len(reposToSelect) == 0 {
		fmt.Println("\n[WARN] No repositories in local database. Please sync or add repositories first.")
		return
	}

	// 4. Select repositories to clone
	var repoOptions []string
	repoMap := make(map[string]domain.Repo)
	for _, r := range reposToSelect {
		optionStr := fmt.Sprintf("%-24s (%s)", r.Name, r.URL)
		repoOptions = append(repoOptions, optionStr)
		repoMap[optionStr] = r
	}

	var selectedOptions []string
	err = survey.AskOne(&survey.MultiSelect{
		Message:  "Select repositories to clone (Arrows to move, Space to toggle, Enter to confirm):",
		Options:  repoOptions,
		PageSize: 10,
	}, &selectedOptions, survey.WithValidator(survey.MinItems(1)))
	if err != nil {
		return
	}

	// 5. Initialize Pipeline
	pipeline := usecase.NewClonePipeline(gitExecutor)
	
	// Register interactive failure handler for Retry/Skip
	pipeline.RegisterFailureHandler(func(job domain.CloneJob, err error) usecase.FailureAction {
		fmt.Printf("\n---------------------------------------------------------\n")
		fmt.Printf("[WARNING] Clone failed for '%s'\n", job.Repo.Name)
		fmt.Printf("Error details: %v\n", err)
		fmt.Printf("---------------------------------------------------------\n")

		var decision string
		prompt := &survey.Select{
			Message: "Choose an action:",
			Options: []string{"Retry (Thử lại)", "Skip (Bỏ qua)"},
		}
		
		if askErr := survey.AskOne(prompt, &decision); askErr != nil {
			return usecase.ActionSkip
		}

		if strings.HasPrefix(decision, "Retry") {
			return usecase.ActionRetry
		}
		return usecase.ActionSkip
	})

	// Add selected repos to pipeline
	for _, opt := range selectedOptions {
		r := repoMap[opt]
		// Keep original service name folder as per specs (keeping names authentic)
		serviceDir := filepath.Join(targetPath, r.Name)
		
		// Authenticate HTTP/HTTPS urls
		if strings.HasPrefix(r.URL, "http://") || strings.HasPrefix(r.URL, "https://") {
			profileID := r.AuthProfileID
			if profileID == "" {
				// Fallback to default
				if defProfile, hasDef := configRepo.GetDefaultAuthProfile(); hasDef {
					profileID = defProfile.ID
				}
			}

			if profileID != "" {
				token, err := keyringRepo.GetToken(profileID)
				if err == nil {
					profile, _ := configRepo.GetAuthProfileByID(profileID)
					// Inject token in http/https URL
					// https://token@github.com/org/repo.git
					// Check if has username
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

		pipeline.AddJob(r, serviceDir)
	}

	// 6. Setup Interrupt (Ctrl+C) trap for graceful exit
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\n\n[SYSTEM] Interrupt signal received! Cancelling pipeline...")
		pipeline.Cancel()
		cancel()
	}()

	fmt.Println("\nStarting clone pipeline (Sequential Mode)...")
	eventChan := pipeline.Start(ctx)

	// Stream events
	for event := range eventChan {
		switch event.EventType {
		case "JOB_STARTED":
			fmt.Printf("\n>>> Cloning %s...\n", event.Payload.RepoName)
		case "CLONE_PROGRESS":
			// Print progress messages inline
			fmt.Printf("  [Progress] %s\n", event.Payload.Message)
		case "JOB_COMPLETED":
			fmt.Printf(">>> Cloning %s... [SUCCESS]\n", event.Payload.RepoName)
		case "JOB_FAILED":
			errCode := "UNKNOWN"
			if event.Payload.ErrorCode != nil {
				errCode = *event.Payload.ErrorCode
			}
			fmt.Printf(">>> Cloning %s... [FAILED] (%s)\n", event.Payload.RepoName, errCode)
		}
	}

	// Print Summary Report
	fmt.Println("\n=========================================================")
	fmt.Println("                  CLONE SUMMARY REPORT                   ")
	fmt.Println("=========================================================")
	
	jobs := pipeline.GetJobs()
	fmt.Printf("| %-24s | %-12s | %-20s |\n", "Service Name", "Status", "Details/Error Code")
	fmt.Println("|--------------------------|--------------|----------------------|")
	for _, j := range jobs {
		statusStr := string(j.State)
		details := "-"
		if j.State == domain.StateFailed {
			details = "ERR_FAILED"
			if j.ErrorMessage != "" {
				msgParts := strings.Split(j.ErrorMessage, ":")
				if len(msgParts) > 0 {
					details = strings.TrimSpace(msgParts[0])
				}
			}
		} else if j.State == domain.StateCancelled {
			details = "ERR_CANCELLED"
		} else if j.State == domain.StateSuccess {
			details = "Cloned OK"
		}
		fmt.Printf("| %-24s | %-12s | %-20s |\n", j.Repo.Name, statusStr, details)
	}
	fmt.Println("=========================================================")
}

func manageReposFlow() {
	for {
		clearScreen()
		fmt.Println("\n--- MANAGE REPOSITORIES ---")
		options := []string{
			"1. List & Search Repos",
			"2. Add New Repo Manually",
			"3. Import Repos from CSV",
			"4. Export CSV Template",
			"5. Delete Repo",
			"6. Back to Main Menu",
		}

		var choice string
		err := survey.AskOne(&survey.Select{
			Message: "Choose repository action:",
			Options: options,
		}, &choice)
		if err != nil {
			return
		}

		if choice == options[0] {
			listReposSubFlow()
		} else if choice == options[1] {
			addRepoManuallySubFlow()
		} else if choice == options[2] {
			importCSVSubFlow()
		} else if choice == options[3] {
			exportCSVTemplateSubFlow()
		} else if choice == options[4] {
			deleteRepoSubFlow()
		} else if choice == options[5] {
			break
		}
		
		fmt.Print("\n[Press Enter to continue]")
		fmt.Scanln()
	}
}

func listReposSubFlow() {
	repos := configRepo.GetRepos()
	if len(repos) == 0 {
		fmt.Println("\nNo repositories in database.")
		return
	}

	fmt.Printf("\nList of %d repositories:\n", len(repos))
	fmt.Println("------------------------------------------------------------------------------------------------------------------------")
	fmt.Printf("%-24s | %-60s | %-15s | %-15s\n", "Name", "Git URL", "Auth Profile", "Tags")
	fmt.Println("------------------------------------------------------------------------------------------------------------------------")
	for _, r := range repos {
		tags := strings.Join(r.Tags, ", ")
		authID := r.AuthProfileID
		if authID == "" {
			authID = "None (Default)"
		}
		fmt.Printf("%-24s | %-60s | %-15s | %-15s\n", r.Name, r.URL, authID, tags)
	}
	fmt.Println("------------------------------------------------------------------------------------------------------------------------")
}

func addRepoManuallySubFlow() {
	var name, url, authProfile, tagsStr string
	
	err := survey.AskOne(&survey.Input{
		Message: "Enter Service Name:",
	}, &name, survey.WithValidator(survey.Required))
	if err != nil {
		return
	}

	err = survey.AskOne(&survey.Input{
		Message: "Enter Git Clone URL (HTTP/HTTPS/SSH):",
	}, &url, survey.WithValidator(func(val interface{}) error {
		str := val.(string)
		if !repository.IsValidGitURL(str) {
			return fmt.Errorf("invalid Git URL format. Must start with http://, https://, git@, or ssh://")
		}
		return nil
	}))
	if err != nil {
		return
	}

	// Fetch auth profiles for suggestion
	profiles := configRepo.GetAuthProfiles()
	var profileChoices = []string{"None (Use Default fallback)"}
	for _, p := range profiles {
		profileChoices = append(profileChoices, p.ID)
	}

	var authChoice string
	err = survey.AskOne(&survey.Select{
		Message: "Select Auth Profile ID:",
		Options: profileChoices,
	}, &authChoice)
	if err != nil {
		return
	}

	if authChoice != profileChoices[0] {
		authProfile = authChoice
	}

	err = survey.AskOne(&survey.Input{
		Message: "Enter tags (separated by semicolons, e.g. go;backend):",
	}, &tagsStr)
	if err != nil {
		return
	}

	tags := repository.ParseTagsCell(tagsStr)

	repo := domain.Repo{
		ID:            uuid.New().String(),
		Name:          name,
		URL:           url,
		AuthProfileID: authProfile,
		Tags:          tags,
		Description:   "",
	}

	err = configRepo.AddRepo(repo)
	if err != nil {
		fmt.Printf("\n[ERROR] Failed to add repository: %v\n", err)
	} else {
		fmt.Println("\n[SUCCESS] Repository added successfully!")
	}
}

func importCSVSubFlow() {
	var path string
	err := survey.AskOne(&survey.Input{
		Message: "Enter path to CSV file:",
	}, &path, survey.WithValidator(survey.Required))
	if err != nil {
		return
	}

	fmt.Printf("\nParsing file %s...\n", path)
	inserted, updated, err := configRepo.ImportReposFromCSV(path)
	if err != nil {
		fmt.Printf("[ERROR] CSV Import failed: %v\n", err)
	} else {
		fmt.Printf("\n[SUCCESS] Imported successfully: Added %d, Upserted %d.\n", inserted, updated)
	}
}

func exportCSVTemplateSubFlow() {
	var path string
	err := survey.AskOne(&survey.Input{
		Message: "Enter target path to save the CSV template (e.g. ./repos_template.csv):",
		Default: "./repos_template.csv",
	}, &path, survey.WithValidator(survey.Required))
	if err != nil {
		return
	}

	err = configRepo.ExportCSVTemplate(path)
	if err != nil {
		fmt.Printf("[ERROR] Failed to save CSV template: %v\n", err)
	} else {
		fmt.Printf("\n[SUCCESS] CSV template saved successfully to %s\n", path)
	}
}

func deleteRepoSubFlow() {
	repos := configRepo.GetRepos()
	if len(repos) == 0 {
		fmt.Println("\nNo repositories to delete.")
		return
	}

	var repoChoices []string
	repoMap := make(map[string]domain.Repo)
	for _, r := range repos {
		opt := fmt.Sprintf("%-24s (%s)", r.Name, r.URL)
		repoChoices = append(repoChoices, opt)
		repoMap[opt] = r
	}

	var choice string
	err := survey.AskOne(&survey.Select{
		Message: "Select repository to delete:",
		Options: repoChoices,
	}, &choice)
	if err != nil {
		return
	}

	r := repoMap[choice]
	err = configRepo.DeleteRepo(r.ID)
	if err != nil {
		fmt.Printf("\n[ERROR] Failed to delete: %v\n", err)
	} else {
		fmt.Println("\n[SUCCESS] Repository deleted successfully!")
	}
}

func manageAuthFlow() {
	for {
		clearScreen()
		fmt.Println("\n--- MANAGE AUTHENTICATION PROFILES ---")
		options := []string{
			"1. List Profiles",
			"2. Create New Profile",
			"3. Delete Profile",
			"4. Back to Main Menu",
		}

		var choice string
		err := survey.AskOne(&survey.Select{
			Message: "Choose authentication action:",
			Options: options,
		}, &choice)
		if err != nil {
			return
		}

		if choice == options[0] {
			listAuthProfilesSubFlow()
		} else if choice == options[1] {
			createAuthProfileSubFlow()
		} else if choice == options[2] {
			deleteAuthProfileSubFlow()
		} else if choice == options[3] {
			break
		}

		fmt.Print("\n[Press Enter to continue]")
		fmt.Scanln()
	}
}

func listAuthProfilesSubFlow() {
	profiles := configRepo.GetAuthProfiles()
	if len(profiles) == 0 {
		fmt.Println("\nNo authentication profiles found.")
		return
	}

	fmt.Printf("\nList of %d profiles:\n", len(profiles))
	fmt.Println("-------------------------------------------------------------------------")
	fmt.Printf("%-15s | %-12s | %-24s | %-10s\n", "Profile ID", "Provider", "Username", "Default")
	fmt.Println("-------------------------------------------------------------------------")
	for _, p := range profiles {
		defaultStr := "No"
		if p.IsDefault {
			defaultStr = "Yes (★)"
		}
		fmt.Printf("%-15s | %-12s | %-24s | %-10s\n", p.ID, p.Name, p.Username, defaultStr)
	}
	fmt.Println("-------------------------------------------------------------------------")
}

func createAuthProfileSubFlow() {
	var id, provider, username, token string
	var isDefault bool

	err := survey.AskOne(&survey.Input{
		Message: "Profile ID (unique identifier, e.g. github-work):",
	}, &id, survey.WithValidator(survey.Required))
	if err != nil {
		return
	}

	err = survey.AskOne(&survey.Select{
		Message: "Provider:",
		Options: []string{"GitHub", "GitLab"},
	}, &provider)
	if err != nil {
		return
	}

	err = survey.AskOne(&survey.Input{
		Message: "Username (optional):",
	}, &username)
	if err != nil {
		return
	}

	err = survey.AskOne(&survey.Password{
		Message: "Enter Personal Access Token (PAT):",
	}, &token, survey.WithValidator(survey.Required))
	if err != nil {
		return
	}

	err = survey.AskOne(&survey.Confirm{
		Message: "Set this profile as Default?",
		Default: false,
	}, &isDefault)
	if err != nil {
		return
	}

	profile := domain.AuthProfile{
		ID:        id,
		Name:      provider,
		Provider:  strings.ToLower(provider),
		Username:  username,
		IsDefault: isDefault,
	}

	// 1. Save PAT securely in Windows Credential Manager
	fmt.Println("\nSaving token securely in Windows Credential Manager...")
	err = keyringRepo.SaveToken(profile.ID, token)
	if err != nil {
		fmt.Printf("[ERROR] Failed to save token in Keychain: %v\n", err)
		return
	}

	// 2. Save metadata in config.json
	err = configRepo.AddAuthProfile(profile)
	if err != nil {
		// Rollback keyring on config error
		_ = keyringRepo.DeleteToken(profile.ID)
		fmt.Printf("[ERROR] Failed to save profile in config: %v\n", err)
	} else {
		fmt.Println("[SUCCESS] Auth Profile created and secured!")
	}
}

func deleteAuthProfileSubFlow() {
	profiles := configRepo.GetAuthProfiles()
	if len(profiles) == 0 {
		fmt.Println("\nNo profiles to delete.")
		return
	}

	var choices []string
	for _, p := range profiles {
		choices = append(choices, p.ID)
	}

	var idToDelete string
	err := survey.AskOne(&survey.Select{
		Message: "Select profile to delete:",
		Options: choices,
	}, &idToDelete)
	if err != nil {
		return
	}

	// 1. Delete token from Keyring
	fmt.Println("\nDeleting token from secure storage...")
	err = keyringRepo.DeleteToken(idToDelete)
	if err != nil {
		fmt.Printf("[WARN] Failed to delete token from Keyring: %v. Proceeding with metadata deletion.\n", err)
	}

	// 2. Delete profile from config
	err = configRepo.DeleteAuthProfile(idToDelete)
	if err != nil {
		fmt.Printf("[ERROR] Failed to delete profile: %v\n", err)
	} else {
		fmt.Println("[SUCCESS] Authentication Profile deleted successfully!")
	}
}

func syncGitFlow() {
	fmt.Println("\n--- SYNC FROM GIT PROVIDERS ---")
	profiles := configRepo.GetAuthProfiles()
	if len(profiles) == 0 {
		fmt.Println("\nNo authentication profiles found. Please create an Auth Profile first.")
		return
	}

	var choices []string
	for _, p := range profiles {
		choices = append(choices, fmt.Sprintf("%s (%s - %s)", p.ID, p.Name, p.Username))
	}

	var selectedChoice string
	err := survey.AskOne(&survey.Select{
		Message: "Select Auth Profile to sync from:",
		Options: choices,
	}, &selectedChoice)
	if err != nil {
		return
	}

	profileID := strings.Split(selectedChoice, " ")[0]

	fmt.Printf("\nSynchronizing repositories via API... (This may take a moment)\n")
	added, updated, err := syncManager.SyncProvider(profileID)
	if err != nil {
		fmt.Printf("\n[ERROR] Sync failed: %v\n", err)
	} else {
		fmt.Printf("\n[SUCCESS] Synchronization complete! Added %d new, Updated %d existing repositories.\n", added, updated)
	}
}

func viewSettingsFlow() {
	cfg := configRepo.GetConfig()

	fmt.Println("\n--- SYSTEM SETTINGS ---")
	fmt.Printf("Default Workspace Root Directory: %s\n", cfg.DefaultRootPath)
	fmt.Printf("Worker Count (Sequential):       %d\n", cfg.WorkerCount)
	fmt.Println("-----------------------")

	var change bool
	err := survey.AskOne(&survey.Confirm{
		Message: "Would you like to modify settings?",
		Default: false,
	}, &change)
	if err != nil {
		return
	}

	if change {
		var root string
		var workers int

		err = survey.AskOne(&survey.Input{
			Message: "Enter default workspace root directory path:",
			Default: cfg.DefaultRootPath,
		}, &root, survey.WithValidator(survey.Required))
		if err != nil {
			return
		}

		err = survey.AskOne(&survey.Input{
			Message: "Enter pipeline worker count:",
			Default: fmt.Sprintf("%d", cfg.WorkerCount),
		}, &workers, survey.WithValidator(survey.Required))
		if err != nil {
			return
		}

		newCfg := domain.WorkspaceConfig{
			DefaultRootPath: strings.TrimSpace(root),
			WorkerCount:     workers,
		}

		err = configRepo.SaveConfig(newCfg)
		if err != nil {
			fmt.Printf("\n[ERROR] Failed to save settings: %v\n", err)
		} else {
			fmt.Println("\n[SUCCESS] Settings saved successfully!")
		}
	}
}

func clearScreen() {
	// Simple console spacing to make interface clean
	fmt.Print("\n\n\n\n")
}
