package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"viktor/internal/client"
)

type InstanceListIn struct{}

type InstanceConnectIn struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

type InstanceDisconnectIn struct {
	Name string `json:"name"`
}

type InstanceSwitchIn struct {
	Name string `json:"name"`
}

type InstanceRenameIn struct {
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

type InstanceCheckHealthIn struct {
	Instance string `json:"instance,omitempty"`
}

type CtxGetIn struct{}

type CtxSetIn struct {
	Instance  string `json:"instance,omitempty"`
	ProjectID int    `json:"project_id,omitempty"`
}

type InstanceInfoOut struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	TokenSet bool   `json:"token_set"`
	Active   bool   `json:"active"`
}

func registerInstance(r *Registry) {
	add[InstanceListIn](r, "instance_list", "List all configured Vikunja instances", handleInstanceList(r))
	add[InstanceConnectIn](r, "instance_connect", "Add and connect a new Vikunja instance", handleInstanceConnect(r))
	add[InstanceDisconnectIn](r, "instance_disconnect", "Remove a configured Vikunja instance", handleInstanceDisconnect(r))
	add[InstanceSwitchIn](r, "instance_switch", "Switch to a different Vikunja instance", handleInstanceSwitch(r))
	add[InstanceRenameIn](r, "instance_rename", "Rename a configured instance", handleInstanceRename(r))
	add[InstanceCheckHealthIn](r, "instance_check_health", "Check if a Vikunja API token is valid", handleInstanceCheckHealth(r))
	add[CtxGetIn](r, "ctx_get", "Get the current default instance and project context", handleCtxGet(r))
	add[CtxSetIn](r, "ctx_set", "Set default instance and project for subsequent queries", handleCtxSet(r))
}

func handleInstanceList(r *Registry) mcp.ToolHandlerFor[InstanceListIn, any] {
	return func(context.Context, *mcp.CallToolRequest, InstanceListIn) (*mcp.CallToolResult, any, error) {
		active, infos := r.listInstances()
		return nil, map[string]any{
			"active":    active,
			"instances": infos,
		}, nil
	}
}

func handleInstanceConnect(r *Registry) mcp.ToolHandlerFor[InstanceConnectIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in InstanceConnectIn) (*mcp.CallToolResult, any, error) {
		if in.Name == "" || in.URL == "" || in.Token == "" {
			return nil, nil, fmt.Errorf("name, url and token are required")
		}
		if err := r.connectInstance(in.Name, in.URL, in.Token); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"name":      in.Name,
			"url":       in.URL,
			"connected": true,
		}, nil
	}
}

func handleInstanceDisconnect(r *Registry) mcp.ToolHandlerFor[InstanceDisconnectIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in InstanceDisconnectIn) (*mcp.CallToolResult, any, error) {
		if err := r.disconnectInstance(in.Name); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"name": in.Name, "removed": true}, nil
	}
}

func handleInstanceSwitch(r *Registry) mcp.ToolHandlerFor[InstanceSwitchIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in InstanceSwitchIn) (*mcp.CallToolResult, any, error) {
		if err := r.switchTo(in.Name); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"name": in.Name, "active": true}, nil
	}
}

func handleInstanceRename(r *Registry) mcp.ToolHandlerFor[InstanceRenameIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in InstanceRenameIn) (*mcp.CallToolResult, any, error) {
		if in.NewName == "" {
			return nil, nil, fmt.Errorf("new_name is required")
		}
		if err := r.renameInstance(in.OldName, in.NewName); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"old_name": in.OldName, "new_name": in.NewName, "renamed": true}, nil
	}
}

func handleInstanceCheckHealth(r *Registry) mcp.ToolHandlerFor[InstanceCheckHealthIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in InstanceCheckHealthIn) (*mcp.CallToolResult, any, error) {
		c, err := r.clientFor(in.Instance)
		if err != nil {
			return nil, nil, err
		}
		h, err := c.Info(ctx)
		if err != nil {
			return nil, map[string]any{
				"instance": in.Instance,
				"valid":    false,
				"error":    err.Error(),
			}, nil
		}
		return nil, map[string]any{
			"instance": in.Instance,
			"valid":    true,
			"version":  h.Version,
		}, nil
	}
}

func handleCtxGet(r *Registry) mcp.ToolHandlerFor[CtxGetIn, any] {
	return func(context.Context, *mcp.CallToolRequest, CtxGetIn) (*mcp.CallToolResult, any, error) {
		name, projectID := r.contextInfo()
		return nil, map[string]any{
			"instance":   name,
			"project_id": float64(projectID),
		}, nil
	}
}

func handleCtxSet(r *Registry) mcp.ToolHandlerFor[CtxSetIn, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in CtxSetIn) (*mcp.CallToolResult, any, error) {
		if in.Instance != "" {
			if err := r.switchTo(in.Instance); err != nil {
				return nil, nil, err
			}
		}
		if in.ProjectID != 0 {
			r.setCtxProject(in.ProjectID)
		}
		name, projectID := r.contextInfo()
		return nil, map[string]any{
			"instance":   name,
			"project_id": float64(projectID),
		}, nil
	}
}

func (r *Registry) listInstances() (string, []InstanceInfoOut) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.clients))
	for name := range r.clients {
		names = append(names, name)
	}
	sort.Strings(names)
	infos := make([]InstanceInfoOut, 0, len(names))
	for _, name := range names {
		c := r.clients[name]
		infos = append(infos, InstanceInfoOut{
			Name:     name,
			URL:      c.BaseURL(),
			TokenSet: true,
			Active:   name == r.active,
		})
	}
	return r.active, infos
}

func (r *Registry) connectInstance(name, url, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.clients[name]; exists {
		return fmt.Errorf("instance %q already exists", name)
	}
	r.clients[name] = client.New(url, token, 0)
	return nil
}

func (r *Registry) disconnectInstance(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == r.active {
		return fmt.Errorf("cannot disconnect active instance %q; switch first", name)
	}
	if _, ok := r.clients[name]; !ok {
		return fmt.Errorf("unknown instance %q", name)
	}
	delete(r.clients, name)
	return nil
}

func (r *Registry) renameInstance(oldName, newName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.clients[oldName]
	if !ok {
		return fmt.Errorf("unknown instance %q", oldName)
	}
	if _, exists := r.clients[newName]; exists {
		return fmt.Errorf("instance %q already exists", newName)
	}
	delete(r.clients, oldName)
	r.clients[newName] = c
	if r.active == oldName {
		r.active = newName
	}
	return nil
}

func (r *Registry) setCtxProject(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctxProj = id
}

func (r *Registry) contextInfo() (string, int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active, r.ctxProj
}
