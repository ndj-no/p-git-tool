package repository

import (
	"errors"
	"workspace-tool/internal/domain"
)

// GetAuthProfileByID retrieves a profile by its ID.
func (r *ConfigRepository) GetAuthProfileByID(id string) (domain.AuthProfile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.authCache {
		if p.ID == id {
			return p, true
		}
	}
	return domain.AuthProfile{}, false
}

// GetDefaultAuthProfile retrieves the profile marked as default.
func (r *ConfigRepository) GetDefaultAuthProfile() (domain.AuthProfile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.authCache {
		if p.IsDefault {
			return p, true
		}
	}
	return domain.AuthProfile{}, false
}

// AddAuthProfile creates a new profile in config. If marked default, resets other profiles' default status.
func (r *ConfigRepository) AddAuthProfile(profile domain.AuthProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check duplicates
	for _, p := range r.authCache {
		if p.ID == profile.ID {
			return errors.New("auth profile with this ID already exists")
		}
	}

	if profile.IsDefault {
		// Reset other defaults
		for i := range r.authCache {
			r.authCache[i].IsDefault = false
		}
	} else if len(r.authCache) == 0 {
		// First profile should be default
		profile.IsDefault = true
	}

	r.authCache = append(r.authCache, profile)
	return r.saveConfigLocked()
}

// UpdateAuthProfile updates a profile's metadata in config.
func (r *ConfigRepository) UpdateAuthProfile(profile domain.AuthProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	foundIdx := -1
	for i, p := range r.authCache {
		if p.ID == profile.ID {
			foundIdx = i
			break
		}
	}

	if foundIdx == -1 {
		return errors.New("auth profile not found")
	}

	if profile.IsDefault {
		// Reset other defaults
		for i := range r.authCache {
			r.authCache[i].IsDefault = false
		}
	}

	r.authCache[foundIdx] = profile

	// Ensure there is at least one default if we had profiles and unset the default one
	if !profile.IsDefault {
		hasDefault := false
		for _, p := range r.authCache {
			if p.IsDefault {
				hasDefault = true
				break
			}
		}
		if !hasDefault && len(r.authCache) > 0 {
			r.authCache[0].IsDefault = true
		}
	}

	return r.saveConfigLocked()
}

// DeleteAuthProfile deletes an auth profile from config.
func (r *ConfigRepository) DeleteAuthProfile(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	foundIdx := -1
	wasDefault := false
	for i, p := range r.authCache {
		if p.ID == id {
			foundIdx = i
			wasDefault = p.IsDefault
			break
		}
	}

	if foundIdx == -1 {
		return errors.New("auth profile not found")
	}

	r.authCache = append(r.authCache[:foundIdx], r.authCache[foundIdx+1:]...)

	// If we deleted the default profile, set the first remaining one as default
	if wasDefault && len(r.authCache) > 0 {
		r.authCache[0].IsDefault = true
	}

	// Update associated repos to remove the deleted profile ID (fallback to default)
	// We'll update the repos cache too so we don't have dangling profile pointers
	// (Note: repos.json file is saved separately, but since we modify cache here, let's save repos as well)
	for i, repo := range r.reposCache {
		if repo.AuthProfileID == id {
			r.reposCache[i].AuthProfileID = ""
		}
	}

	if err := r.saveReposLocked(); err != nil {
		r.logger.Warn("Failed to save repos after profile deletion: %v", err)
	}

	return r.saveConfigLocked()
}
