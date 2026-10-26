package tools

type ProjectListIn struct {
	Instance string `json:"instance,omitempty"`
}

type ProjectListAllIn struct{}

type ProjectGetIn struct {
	ProjectID int `json:"project_id"`
}

type ProjectCreateIn struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type ProjectUpdateIn struct {
	ProjectID   int    `json:"project_id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type ProjectDeleteIn struct {
	ProjectID int  `json:"project_id"`
	Confirm   bool `json:"confirm"`
}

type ProjectShareIn struct {
	ProjectID  int    `json:"project_id"`
	UserID     int    `json:"user_id,omitempty"`
	Username   string `json:"username,omitempty"`
	Permission int    `json:"permission,omitempty"`
}

type ProjectSetupIn struct {
	ProjectID int      `json:"project_id"`
	Buckets   []string `json:"buckets,omitempty"`
	// Prune deletes existing buckets that are not in the requested
	// profile. Off by default: pruning is destructive on a project with
	// hand-made columns (card #891). Empty extra buckets are still
	// removable; non-empty ones are never deleted.
	Prune bool `json:"prune,omitempty"`
}

type ProjectAnalyzeIn struct {
	ProjectID int `json:"project_id"`
}

type ProjectCreateFromTemplateIn struct {
	Title string `json:"title"`
	Mode  string `json:"mode,omitempty"`
}

type ProjectExportIn struct{}

type ProjectImportIn struct {
	ExportJSON   string `json:"export_json"`
	SkipExisting bool   `json:"skip_existing,omitempty"`
}

func registerProject(r *Registry) {
	add[ProjectListIn](r, "project_list", "List projects on the current instance", handleProjectList(r))
	add[ProjectListAllIn](r, "project_list_all", "List projects across all configured instances", handleProjectListAll(r))
	add[ProjectGetIn](r, "project_get", "Get project details", handleProjectGet(r))
	add[ProjectCreateIn](r, "project_create", "Create a project", handleProjectCreate(r))
	add[ProjectUpdateIn](r, "project_update", "Update a project", handleProjectUpdate(r))
	add[ProjectDeleteIn](r, "project_delete", "Delete a project and all its tasks permanently", handleProjectDelete(r))
	add[ProjectShareIn](r, "project_share", "Share a project with a user; resolves user_id/username pair automatically (Vikunja needs both)", handleProjectShare(r))
	add[ProjectShareTeamIn](r, "project_share_team", "Share a project with a team, list team shares, or revoke access (action: share|list|revoke; permission 0=read 1=write 2=admin)", handleProjectShareTeam(r))
	add[ProjectSetupIn](r, "project_setup", "Set up a project with buckets, labels, and tasks in one call", handleProjectSetup(r))
	add[ProjectAnalyzeIn](r, "project_analyze", "Analyze project data to discover meaningful grouping dimensions", handleProjectAnalyze(r))
	add[ProjectCreateFromTemplateIn](r, "project_create_from_template", "Create a project from a template with exactly the requested kanban columns", handleProjectCreateFromTemplate(r))
	add[ProjectExportIn](r, "project_export", "Export all projects and tasks for backup")
	add[ProjectImportIn](r, "project_import", "Import projects from an export file")
}
