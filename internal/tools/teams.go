package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type TeamsListIn struct {
	Instance string `json:"instance,omitempty"`
}

type TeamsSearchIn struct {
	Query    string `json:"query"`
	Instance string `json:"instance,omitempty"`
}

type TeamsCreateIn struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Instance    string `json:"instance,omitempty"`
}

type TeamsUpdateIn struct {
	TeamID      int    `json:"team_id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Instance    string `json:"instance,omitempty"`
}

type TeamsDeleteIn struct {
	TeamID   int    `json:"team_id"`
	Instance string `json:"instance,omitempty"`
}

type TeamsMemberAddIn struct {
	TeamID   int    `json:"team_id"`
	Username string `json:"username"`
	Instance string `json:"instance,omitempty"`
}

type TeamsMemberRemoveIn struct {
	TeamID   int    `json:"team_id"`
	Username string `json:"username"`
	Instance string `json:"instance,omitempty"`
}

func trimTeam(t map[string]any, withMembers bool) map[string]any {
	keys := []string{"id", "name", "description", "is_public"}
	out := make(map[string]any, len(keys)+1)
	for _, k := range keys {
		if v, ok := t[k]; ok {
			out[k] = v
		}
	}
	if withMembers {
		members, _ := t["members"].([]any)
		if members == nil {
			members = []any{}
		}
		out["members"] = members
		out["member_count"] = len(members)
	}
	return out
}

var teamNameRe = regexp.MustCompile(`^[^\s/]+$`)

func handleTeamsList(r *Registry) mcp.ToolHandlerFor[TeamsListIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TeamsListIn) (*mcp.CallToolResult, any, error) {
		c, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		var teams []map[string]any
		if err := c.ListTeams(ctx, "", &teams); err != nil {
			return nil, nil, err
		}
		trimmed := make([]map[string]any, len(teams))
		for i, t := range teams {
			trimmed[i] = trimTeam(t, true)
		}
		return nil, map[string]any{"count": len(trimmed), "teams": trimmed}, nil
	}
}

func handleTeamsSearch(r *Registry) mcp.ToolHandlerFor[TeamsSearchIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TeamsSearchIn) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, nil, fmt.Errorf("query is required")
		}
		c, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		var teams []map[string]any
		if err := c.ListTeams(ctx, in.Query, &teams); err != nil {
			return nil, nil, err
		}
		trimmed := make([]map[string]any, len(teams))
		for i, t := range teams {
			trimmed[i] = trimTeam(t, true)
		}
		return nil, map[string]any{"count": len(trimmed), "teams": trimmed}, nil
	}
}

func handleTeamsCreate(r *Registry) mcp.ToolHandlerFor[TeamsCreateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TeamsCreateIn) (*mcp.CallToolResult, any, error) {
		if !teamNameRe.MatchString(in.Name) {
			return nil, nil, fmt.Errorf("name is required and must not contain spaces")
		}
		body := map[string]any{"name": in.Name}
		if in.Description != "" {
			body["description"] = in.Description
		}
		var created map[string]any
		if err := r.api().CreateTeam(ctx, body, &created); err != nil {
			return nil, nil, err
		}
		return nil, trimTeam(created, false), nil
	}
}

func handleTeamsUpdate(r *Registry) mcp.ToolHandlerFor[TeamsUpdateIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TeamsUpdateIn) (*mcp.CallToolResult, any, error) {
		if in.Name == "" && in.Description == "" {
			return nil, nil, fmt.Errorf("nothing to update: provide name or description")
		}
		var current map[string]any
		if err := r.api().GetTeam(ctx, in.TeamID, &current); err != nil {
			return nil, nil, err
		}
		body := map[string]any{"name": in.Name}
		if body["name"] == "" {
			body["name"] = current["name"]
		}
		if in.Description != "" {
			body["description"] = in.Description
		} else if d, ok := current["description"].(string); ok {
			body["description"] = d
		}
		var updated map[string]any
		if err := r.api().UpdateTeam(ctx, in.TeamID, body, &updated); err != nil {
			return nil, nil, err
		}
		return nil, trimTeam(updated, false), nil
	}
}

func handleTeamsDelete(r *Registry) mcp.ToolHandlerFor[TeamsDeleteIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TeamsDeleteIn) (*mcp.CallToolResult, any, error) {
		if err := r.api().DeleteTeam(ctx, in.TeamID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"deleted": true, "team_id": in.TeamID}, nil
	}
}

func handleTeamsMemberAdd(r *Registry) mcp.ToolHandlerFor[TeamsMemberAddIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TeamsMemberAddIn) (*mcp.CallToolResult, any, error) {
		if in.Username == "" {
			return nil, nil, fmt.Errorf("username is required (user_id alone is not accepted by Vikunja)")
		}
		if !teamNameRe.MatchString(in.Username) {
			return nil, nil, fmt.Errorf("invalid username")
		}
		var added map[string]any
		if err := r.api().AddTeamMember(ctx, in.TeamID, in.Username, &added); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"team_id":  in.TeamID,
			"username": added["username"],
			"admin":    added["admin"],
		}, nil
	}
}

func handleTeamsMemberRemove(r *Registry) mcp.ToolHandlerFor[TeamsMemberRemoveIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TeamsMemberRemoveIn) (*mcp.CallToolResult, any, error) {
		if in.Username == "" {
			return nil, nil, fmt.Errorf("username is required")
		}
		if !teamNameRe.MatchString(in.Username) {
			return nil, nil, fmt.Errorf("invalid username")
		}
		if err := r.api().RemoveTeamMember(ctx, in.TeamID, in.Username); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"removed": true, "team_id": in.TeamID, "username": in.Username}, nil
	}
}

func registerTeams(r *Registry) {
	add[TeamsListIn](r, "teams_list", "List all teams the authenticated user belongs to", handleTeamsList(r))
	add[TeamsSearchIn](r, "teams_search", "Search teams by name substring", handleTeamsSearch(r))
	add[TeamsCreateIn](r, "teams_create", "Create a team (PUT semantics: creator becomes admin)", handleTeamsCreate(r))
	add[TeamsUpdateIn](r, "teams_update", "Update a team name or description", handleTeamsUpdate(r))
	add[TeamsDeleteIn](r, "teams_delete", "Delete a team permanently", handleTeamsDelete(r))
	add[TeamsMemberAddIn](r, "teams_member_add", "Add a user to a team by username", handleTeamsMemberAdd(r))
	add[TeamsMemberRemoveIn](r, "teams_member_remove", "Remove a user from a team by username", handleTeamsMemberRemove(r))
}
