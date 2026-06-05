import './style.css';
import './app.css';

// Import Wails bound Go backend methods
import {
    GetConfig,
    SaveConfig,
    GetRepos,
    AddRepo,
    UpdateRepo,
    DeleteRepo,
    ImportCSV,
    ExportCSVTemplate,
    GetAuthProfiles,
    AddAuthProfile,
    DeleteAuthProfile,
    SyncProvider,
    StartClone,
    CancelClone,
    SendFailureResponse
} from '../wailsjs/go/main/App';

// Import Wails runtime features
import { EventsOn } from '../wailsjs/runtime/runtime';

// Global application state
const state = {
    config: { default_root_path: '', worker_count: 1 },
    repos: [],
    profiles: [],
    selectedRepoIDs: new Set(),
    activeCloneJobs: [],
    pipelineTotalJobs: 0
};

// SVG icons loaded locally as standard inlined elements
const icons = {
    edit: `<svg class="table-action-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 1 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>`,
    delete: `<svg class="table-action-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg>`
};

document.addEventListener('DOMContentLoaded', () => {
    initRouting();
    initData();
    initDashboardFlow();
    initRepoCRUDFlow();
    initAuthFlow();
    initSyncFlow();
    initSettingsFlow();
    initLogTerminalFeatures();
});

// --------------------------------------------------------------------
// 1. SPA Routing & Sidebar Navigation
// --------------------------------------------------------------------
function initRouting() {
    const navItems = document.querySelectorAll('.nav-item');
    const tabPanels = document.querySelectorAll('.tab-panel');

    navItems.forEach(item => {
        item.addEventListener('click', () => {
            const targetTab = item.getAttribute('data-tab');

            // Toggle active sidebar link
            navItems.forEach(nav => nav.classList.remove('active'));
            item.classList.add('active');

            // Swap active panel
            tabPanels.forEach(panel => {
                panel.classList.remove('active');
                if (panel.id === targetTab) {
                    panel.classList.add('active');
                }
            });

            // Re-fetch data on tab switch to stay in sync
            refreshDataForTab(targetTab);
        });
    });
}

function refreshDataForTab(tabId) {
    if (tabId === 'repos-tab') {
        loadRepositoriesTable();
    } else if (tabId === 'auth-tab') {
        loadAuthProfiles();
    } else if (tabId === 'sync-tab') {
        loadSyncProfilesDropdown();
    } else if (tabId === 'dashboard-tab') {
        loadDashboardRepoPicker();
    }
}

// --------------------------------------------------------------------
// 2. Data Initialization
// --------------------------------------------------------------------
async function initData() {
    try {
        // Load configurations
        const cfg = await GetConfig();
        state.config = cfg;

        // Pre-fill target path computation elements
        const rootPathElem = document.getElementById('target-path-input');
        if (rootPathElem) {
            rootPathElem.value = state.config.default_root_path;
        }

        // Fetch repository cache
        const repos = await GetRepos();
        state.repos = repos;

        // Fetch auth profile cache
        const profiles = await GetAuthProfiles();
        state.profiles = profiles;

        // Render initial view elements
        loadDashboardRepoPicker();
        loadRepositoriesTable();
        loadAuthProfiles();
        loadSyncProfilesDropdown();
        loadSettingsTabFields();

    } catch (err) {
        console.error("Initialization failed:", err);
    }
}

// --------------------------------------------------------------------
// 3. Dashboard & Clone Pipeline Flow
// --------------------------------------------------------------------
function initDashboardFlow() {
    const taskInput = document.getElementById('task-name-input');
    const targetInput = document.getElementById('target-path-input');
    const resetBtn = document.getElementById('reset-path-btn');
    const filterCheckbox = document.getElementById('filter-repos-checkbox');
    const filterField = document.getElementById('filter-tag-field');
    const filterTagInput = document.getElementById('filter-tag-input');
    const searchInput = document.getElementById('repo-picker-search');
    const startBtn = document.getElementById('start-clone-btn');
    const cancelBtn = document.getElementById('cancel-clone-btn');

    // Autocompute destination path on task typing
    taskInput.addEventListener('input', () => {
        const task = taskInput.value.trim();
        if (task !== "") {
            targetInput.value = state.config.default_root_path + "\\" + task;
        } else {
            targetInput.value = state.config.default_root_path;
        }
    });

    // Reset path button
    resetBtn.addEventListener('click', () => {
        const task = taskInput.value.trim();
        if (task !== "") {
            targetInput.value = state.config.default_root_path + "\\" + task;
        } else {
            targetInput.value = state.config.default_root_path;
        }
    });

    // Tag filtering checkbox
    filterCheckbox.addEventListener('change', () => {
        if (filterCheckbox.checked) {
            filterField.classList.remove('hidden');
        } else {
            filterField.classList.add('hidden');
            filterTagInput.value = "";
            loadDashboardRepoPicker(); // Reset filter
        }
    });

    filterTagInput.addEventListener('input', loadDashboardRepoPicker);
    searchInput.addEventListener('input', loadDashboardRepoPicker);

    // Start Clone Pipeline Action
    startBtn.addEventListener('click', async () => {
        const taskName = taskInput.value.trim();
        const targetPath = targetInput.value.trim();

        if (taskName === "" || targetPath === "" || state.selectedRepoIDs.size === 0) {
            alert("Please complete task name, destination, and select at least 1 repository.");
            return;
        }

        // Show Execution Overlay Viewport
        const overlay = document.getElementById('clone-execution-overlay');
        overlay.classList.remove('hidden');

        document.getElementById('overlay-task-title').innerText = `Cloning ${taskName}`;
        document.getElementById('overlay-target-path').innerText = `Target: ${targetPath}`;

        // Reset progress trackers
        state.activeCloneJobs = [];
        state.pipelineTotalJobs = state.selectedRepoIDs.size;
        updateOverallProgressBar(0, `Processing 0/${state.pipelineTotalJobs} Repositories`);

        // Populate execution job cards
        const container = document.getElementById('clone-repo-states-container');
        container.innerHTML = "";

        const repoMap = {};
        state.repos.forEach(r => repoMap[r.id] = r);

        Array.from(state.selectedRepoIDs).forEach(id => {
            const r = repoMap[id];
            state.activeCloneJobs.push({ id: r.id, name: r.name, state: 'PENDING' });

            const card = document.createElement('div');
            card.className = 'clone-status-box PENDING';
            card.id = `job-card-${r.id}`;
            card.innerHTML = `
                <span class="clone-status-circle"></span>
                <div class="clone-status-meta">
                    <span class="clone-status-name">${r.name}</span>
                    <span class="clone-status-lbl" id="job-lbl-${r.id}">Pending</span>
                </div>
            `;
            container.appendChild(card);
        });

        // Clear terminal viewport
        const terminalBody = document.getElementById('terminal-body-elem');
        terminalBody.innerHTML = `<div class="terminal-line system">>> Clone pipeline worker started successfully...</div>`;

        // Start asynchronous Wails call
        const repoIDs = Array.from(state.selectedRepoIDs);
        const err = await StartClone(taskName, targetPath, repoIDs);
        if (err !== "") {
            appendTerminalLine(`Error starting clone: ${err}`, 'error');
            alert(err);
        }
    });

    // Cancel / Graceful exit button
    cancelBtn.addEventListener('click', () => {
        CancelClone();
        document.getElementById('clone-execution-overlay').classList.add('hidden');
    });

    // Subscribe to Wails event streams
    EventsOn('clone_event', handleCloneEvent);
    EventsOn('clone_error_prompt', handleCloneErrorPrompt);
}

function loadDashboardRepoPicker() {
    const listContainer = document.getElementById('repo-picker-list-container');
    const selectedCountElem = document.getElementById('selected-repos-count');
    const startBtn = document.getElementById('start-clone-btn');
    const searchVal = document.getElementById('repo-picker-search').value.trim().toLowerCase();
    const tagFilterChecked = document.getElementById('filter-repos-checkbox').checked;
    const tagVal = document.getElementById('filter-tag-input').value.trim().toLowerCase();

    listContainer.innerHTML = "";

    // Filter repos cache based on search inputs
    const filtered = state.repos.filter(r => {
        const matchesSearch = r.name.toLowerCase().includes(searchVal) || r.url.toLowerCase().includes(searchVal);
        
        let matchesTags = true;
        if (tagFilterChecked && tagVal !== "") {
            matchesTags = r.tags.some(t => t.toLowerCase().includes(tagVal));
        }

        return matchesSearch && matchesTags;
    });

    if (filtered.length === 0) {
        listContainer.innerHTML = `<div class="empty-state">No matching repositories found.</div>`;
        return;
    }

    filtered.forEach(r => {
        const isChecked = state.selectedRepoIDs.has(r.id);
        const item = document.createElement('div');
        item.className = 'repo-picker-item';
        
        item.innerHTML = `
            <label class="checkbox-container">
                <input type="checkbox" class="repo-picker-checkbox" data-id="${r.id}" ${isChecked ? 'checked' : ''}/>
                <span class="checkbox-checkmark"></span>
            </label>
            <div class="repo-meta flex-1">
                <span class="repo-name-text">${r.name}</span>
                <span class="repo-url-text">${r.url}</span>
                <div class="repo-pills-row">
                    ${r.tags.map(t => `<span class="tag-pill">${t}</span>`).join('')}
                </div>
            </div>
        `;

        // Bind checkbox click toggle
        const checkbox = item.querySelector('.repo-picker-checkbox');
        checkbox.addEventListener('change', () => {
            if (checkbox.checked) {
                state.selectedRepoIDs.add(r.id);
            } else {
                state.selectedRepoIDs.delete(r.id);
            }
            // Update selected counts
            selectedCountElem.innerText = `${state.selectedRepoIDs.size} Selected`;
            startBtn.disabled = state.selectedRepoIDs.size === 0;
        });

        // Whole card click triggers toggle
        item.addEventListener('click', (e) => {
            if (e.target.tagName !== 'INPUT' && e.target.tagName !== 'SPAN') {
                checkbox.checked = !checkbox.checked;
                checkbox.dispatchEvent(new Event('change'));
            }
        });

        listContainer.appendChild(item);
    });

    selectedCountElem.innerText = `${state.selectedRepoIDs.size} Selected`;
    startBtn.disabled = state.selectedRepoIDs.size === 0;
}

// --------------------------------------------------------------------
// 4. Real-time Clone Event Processing
// --------------------------------------------------------------------
function handleCloneEvent(event) {
    const payload = event.payload;
    const repoID = payload.repo_id;
    const stateStr = payload.state;
    const message = payload.message;

    // Update job card UI status classes
    const card = document.getElementById(`job-card-${repoID}`);
    const lbl = document.getElementById(`job-lbl-${repoID}`);
    
    if (card && lbl) {
        card.className = `clone-status-box ${stateStr}`;
        lbl.innerText = stateStr;
    }

    // Stream logs inside Terminal logger viewport
    switch (event.event_type) {
        case 'JOB_STARTED':
            appendTerminalLine(`\n[${payload.repo_name}] >>> Starting Clone operation...`, 'system');
            break;
        case 'CLONE_PROGRESS':
            appendTerminalLine(`[${payload.repo_name}] ${message}`, 'progress');
            break;
        case 'JOB_COMPLETED':
            appendTerminalLine(`[${payload.repo_name}] >>> [SUCCESS] Repository cloned successfully.`, 'success');
            break;
        case 'JOB_FAILED':
            let errCode = payload.error_code ? payload.error_code : "UNKNOWN_ERROR";
            appendTerminalLine(`[${payload.repo_name}] >>> [FAILED] ${message} (${errCode})`, 'error');
            break;
    }

    // Recalculate pipeline total progress bar fills
    const finishedCount = state.activeCloneJobs.filter(j => {
        // If it's the current event, update its cached status
        if (j.id === repoID) {
            j.state = stateStr;
        }
        return j.state === 'SUCCESS' || j.state === 'FAILED' || j.state === 'CANCELLED';
    }).length;

    const percent = Math.round((finishedCount / state.pipelineTotalJobs) * 100);
    updateOverallProgressBar(percent, `Processing ${finishedCount}/${state.pipelineTotalJobs} Repositories`);
}

function updateOverallProgressBar(percent, text) {
    document.getElementById('progress-meta-status').innerText = text;
    document.getElementById('progress-meta-percent').innerText = `${percent}%`;
    document.getElementById('progress-bar-fill-elem').style.width = `${percent}%`;
}

function appendTerminalLine(text, className) {
    const body = document.getElementById('terminal-body-elem');
    const div = document.createElement('div');
    div.className = `terminal-line ${className || ''}`;
    div.innerText = text;
    body.appendChild(div);

    // Auto scroll to bottom
    body.scrollTop = body.scrollHeight;
}

function initLogTerminalFeatures() {
    document.getElementById('terminal-clear-btn').addEventListener('click', () => {
        document.getElementById('terminal-body-elem').innerHTML = "";
    });
}

// --------------------------------------------------------------------
// 5. Interactive Recovery Modal Prompt (Retry/Skip)
// --------------------------------------------------------------------
function handleCloneErrorPrompt(data) {
    const modal = document.getElementById('recovery-modal');
    modal.classList.remove('hidden');

    document.getElementById('recovery-repo-title').innerText = `Job Failed: ${data.repo_name}`;
    document.getElementById('recovery-error-desc').innerText = `Error Details: ${data.error}`;

    const retryBtn = document.getElementById('recovery-retry-btn');
    const skipBtn = document.getElementById('recovery-skip-btn');

    // Clean listeners by cloneNode replacement
    const newRetry = retryBtn.cloneNode(true);
    const newSkip = skipBtn.cloneNode(true);

    retryBtn.parentNode.replaceChild(newRetry, retryBtn);
    skipBtn.parentNode.replaceChild(newSkip, skipBtn);

    newRetry.addEventListener('click', () => {
        SendFailureResponse("retry");
        modal.classList.add('hidden');
    });

    newSkip.addEventListener('click', () => {
        SendFailureResponse("skip");
        modal.classList.add('hidden');
    });
}

// --------------------------------------------------------------------
// 6. Repository CRUD Flow & CSV Import
// --------------------------------------------------------------------
function initRepoCRUDFlow() {
    const searchInput = document.getElementById('repo-table-search');
    const modal = document.getElementById('repo-modal');
    const modalTitle = document.getElementById('repo-modal-title');
    const addBtn = document.getElementById('add-repo-modal-btn');
    const cancelBtn = document.getElementById('repo-modal-cancel');
    const saveBtn = document.getElementById('repo-modal-save');
    const csvBtn = document.getElementById('csv-import-btn');
    const csvTemplateBtn = document.getElementById('csv-template-btn');

    searchInput.addEventListener('input', loadRepositoriesTable);

    // Show Create modal
    addBtn.addEventListener('click', () => {
        modalTitle.innerText = "Add Repository";
        document.getElementById('repo-modal-id').value = "";
        document.getElementById('repo-modal-name').value = "";
        document.getElementById('repo-modal-url').value = "";
        document.getElementById('repo-modal-tags').value = "";

        populateAuthProfileDropdown('repo-modal-profile');

        modal.classList.remove('hidden');
    });

    cancelBtn.addEventListener('click', () => modal.classList.add('hidden'));

    // Save Action
    saveBtn.addEventListener('click', async () => {
        const id = document.getElementById('repo-modal-id').value.trim();
        const name = document.getElementById('repo-modal-name').value.trim();
        const url = document.getElementById('repo-modal-url').value.trim();
        const authProfile = document.getElementById('repo-modal-profile').value;
        const tags = document.getElementById('repo-modal-tags').value.trim();

        if (name === "" || url === "") {
            alert("Name and URL are required.");
            return;
        }

        let err = "";
        if (id === "") {
            // Create manually
            err = await AddRepo(name, url, authProfile === "none" ? "" : authProfile, tags);
        } else {
            // Edit update
            err = await UpdateRepo(id, name, url, authProfile === "none" ? "" : authProfile, tags);
        }

        if (err !== "") {
            alert(err);
        } else {
            modal.classList.add('hidden');
            // Refresh caches
            state.repos = await GetRepos();
            loadRepositoriesTable();
        }
    });

    // CSV Bulk Import Action
    csvBtn.addEventListener('click', async () => {
        const result = await ImportCSV();
        if (result === "") return; // User cancelled
        alert(result);
        
        state.repos = await GetRepos();
        loadRepositoriesTable();
    });

    // CSV Template Export Action
    csvTemplateBtn.addEventListener('click', async () => {
        const result = await ExportCSVTemplate();
        if (result === "") return; // User cancelled
        alert(result);
    });
}

function populateAuthProfileDropdown(dropdownId, selectedID) {
    const select = document.getElementById(dropdownId);
    select.innerHTML = `<option value="none">None (Use Default fallback)</option>`;

    state.profiles.forEach(p => {
        const isSelected = p.id === selectedID;
        select.innerHTML += `<option value="${p.id}" ${isSelected ? 'selected' : ''}>${p.id} (${p.name})</option>`;
    });
}

function loadRepositoriesTable() {
    const tbody = document.getElementById('repos-table-body');
    const searchVal = document.getElementById('repo-table-search').value.trim().toLowerCase();

    tbody.innerHTML = "";

    const filtered = state.repos.filter(r => {
        return r.name.toLowerCase().includes(searchVal) || 
               r.url.toLowerCase().includes(searchVal) || 
               r.tags.some(t => t.toLowerCase().includes(searchVal));
    });

    if (filtered.length === 0) {
        tbody.innerHTML = `<tr><td colspan="5" class="empty-state">No repositories stored in local database.</td></tr>`;
        return;
    }

    filtered.forEach(r => {
        const tr = document.createElement('tr');
        const tagsHTML = r.tags.map(t => `<span class="tag-pill">${t}</span>`).join(' ');
        const authStr = r.auth_profile_id ? r.auth_profile_id : '<span class="color-text-darker">None (Default)</span>';

        tr.innerHTML = `
            <td><strong>${r.name}</strong></td>
            <td><code class="font-mono text-xs">${r.url}</code></td>
            <td>${authStr}</td>
            <td>${tagsHTML}</td>
            <td class="text-right">
                <div class="actions-cell-row">
                    <button class="table-action-btn edit" data-id="${r.id}">
                        ${icons.edit}
                    </button>
                    <button class="table-action-btn delete" data-id="${r.id}">
                        ${icons.delete}
                    </button>
                </div>
            </td>
        `;

        // Bind Edit button
        tr.querySelector('.table-action-btn.edit').addEventListener('click', () => {
            const modal = document.getElementById('repo-modal');
            const modalTitle = document.getElementById('repo-modal-title');

            modalTitle.innerText = "Edit Repository";
            document.getElementById('repo-modal-id').value = r.id;
            document.getElementById('repo-modal-name').value = r.name;
            document.getElementById('repo-modal-url').value = r.url;
            document.getElementById('repo-modal-tags').value = r.tags.join(';');

            populateAuthProfileDropdown('repo-modal-profile', r.auth_profile_id);

            modal.classList.remove('hidden');
        });

        // Bind Delete button
        tr.querySelector('.table-action-btn.delete').addEventListener('click', async () => {
            if (confirm(`Are you sure you want to delete repository '${r.name}'?`)) {
                const err = await DeleteRepo(r.id);
                if (err !== "") {
                    alert(err);
                } else {
                    state.repos = await GetRepos();
                    loadRepositoriesTable();
                }
            }
        });

        tbody.appendChild(tr);
    });
}

// --------------------------------------------------------------------
// 7. Authentication Profiles Flow
// --------------------------------------------------------------------
function initAuthFlow() {
    const modal = document.getElementById('auth-modal');
    const addBtn = document.getElementById('add-auth-modal-btn');
    const cancelBtn = document.getElementById('auth-modal-cancel');
    const saveBtn = document.getElementById('auth-modal-save');

    addBtn.addEventListener('click', () => {
        document.getElementById('auth-modal-id').value = "";
        document.getElementById('auth-modal-provider').value = "GitHub";
        document.getElementById('auth-modal-user').value = "";
        document.getElementById('auth-modal-token').value = "";
        document.getElementById('auth-modal-default').checked = false;

        modal.classList.remove('hidden');
    });

    cancelBtn.addEventListener('click', () => modal.classList.add('hidden'));

    saveBtn.addEventListener('click', async () => {
        const id = document.getElementById('auth-modal-id').value.trim();
        const provider = document.getElementById('auth-modal-provider').value;
        const username = document.getElementById('auth-modal-user').value.trim();
        const token = document.getElementById('auth-modal-token').value;
        const isDefault = document.getElementById('auth-modal-default').checked;

        if (id === "" || token === "") {
            alert("Profile ID and Personal Access Token (PAT) are required.");
            return;
        }

        const err = await AddAuthProfile(id, provider, username, token, isDefault);
        if (err !== "") {
            alert(err);
        } else {
            modal.classList.add('hidden');
            state.profiles = await GetAuthProfiles();
            loadAuthProfiles();
        }
    });
}

function loadAuthProfiles() {
    const container = document.getElementById('auth-profiles-grid-container');
    container.innerHTML = "";

    if (state.profiles.length === 0) {
        container.innerHTML = `<div class="empty-state flex-grow">No authentication profiles found. Add one above.</div>`;
        return;
    }

    state.profiles.forEach(p => {
        const card = document.createElement('div');
        card.className = `auth-card ${p.is_default ? 'default' : ''}`;
        
        const provClass = p.provider === 'github' ? 'github' : 'gitlab';

        card.innerHTML = `
            <div class="auth-card-header">
                <span class="provider-badge ${provClass}">${p.name}</span>
                ${p.is_default ? '<span class="default-star-badge">Default Fallback</span>' : ''}
            </div>
            <div class="auth-card-body flex-1">
                <span class="auth-card-title">${p.id}</span>
                <span class="auth-card-user">User: <strong>${p.username ? p.username : 'git'}</strong></span>
            </div>
            <div class="auth-card-footer">
                <button class="btn btn-secondary table-action-btn delete" data-id="${p.id}">
                    Delete Profile
                </button>
            </div>
        `;

        card.querySelector('.delete').addEventListener('click', async () => {
            if (confirm(`Are you sure you want to delete profile '${p.id}'? This will also remove the token from Windows Credential Manager.`)) {
                const err = await DeleteAuthProfile(p.id);
                if (err !== "") {
                    alert(err);
                } else {
                    state.profiles = await GetAuthProfiles();
                    loadAuthProfiles();
                }
            }
        });

        container.appendChild(card);
    });
}

// --------------------------------------------------------------------
// 8. Git Provider API Synchronization
// --------------------------------------------------------------------
function initSyncFlow() {
    const runBtn = document.getElementById('run-sync-btn');
    
    runBtn.addEventListener('click', async () => {
        const select = document.getElementById('sync-profile-select');
        const selectedID = select.value;

        if (selectedID === "" || selectedID === null) {
            alert("Please create and select an Authentication Profile first.");
            return;
        }

        runBtn.disabled = true;
        runBtn.innerHTML = `Syncing...`;

        try {
            const result = await SyncProvider(selectedID);
            alert(result);
            // Reload repos
            state.repos = await GetRepos();
        } catch(err) {
            alert(err);
        } finally {
            runBtn.disabled = false;
            runBtn.innerHTML = `<svg class="btn-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/></svg> Sync via API`;
        }
    });
}

function loadSyncProfilesDropdown() {
    const select = document.getElementById('sync-profile-select');
    select.innerHTML = "";

    if (state.profiles.length === 0) {
        select.innerHTML = `<option value="">Create an Auth Profile first</option>`;
        return;
    }

    state.profiles.forEach(p => {
        select.innerHTML += `<option value="${p.id}">${p.id} (${p.name} - ${p.username ? p.username : 'git'})</option>`;
    });
}

// --------------------------------------------------------------------
// 9. System Settings Flow
// --------------------------------------------------------------------
function initSettingsFlow() {
    const saveBtn = document.getElementById('save-settings-btn');
    
    saveBtn.addEventListener('click', async () => {
        const path = document.getElementById('setting-root-path').value.trim();
        const workers = parseInt(document.getElementById('setting-workers').value, 10);

        if (path === "") {
            alert("Default root path is required.");
            return;
        }

        const err = await SaveConfig(path, workers);
        if (err !== "") {
            alert(err);
        } else {
            alert("Settings saved successfully!");
            state.config.default_root_path = path;
            state.config.worker_count = workers;
        }
    });
}

function loadSettingsTabFields() {
    const pathInput = document.getElementById('setting-root-path');
    const workersInput = document.getElementById('setting-workers');

    if (pathInput && workersInput) {
        pathInput.value = state.config.default_root_path;
        workersInput.value = state.config.worker_count;
    }
}
