package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"whirlwind/internal/model"
)

const (
	codexCatalogBatchSize = 100
	codexCatalogThreadID  = "threadId"
	codexAppPrefix        = "app://"
)

type codexAppSummary struct{ ID, Name string }
type codexInstalledApp struct {
	ID                string
	Enabled, Callable bool
}
type codexCatalogPage[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"nextCursor"`
}
type codexAppTool struct {
	Name, Description     string
	IsEnabled, IsReadOnly bool
}
type codexAppMetadata struct {
	ID, Name      string
	ToolSummaries []codexAppTool
}
type codexAppReadResponse struct {
	Apps          []codexAppMetadata
	MissingAppIDs []string
}
type codexMCPTool struct {
	Name, Description string
	Annotations       map[string]any
}
type codexMCPStatus struct {
	Name                    string
	Tools                   map[string]codexMCPTool
	ToolsError              *string
	AuthStatus              string
	RuntimeStatus, PluginID *string
}

func codexAppCatalog(provider string, apps, installed json.RawMessage) ([]model.Integration, error) {
	var list struct{ Data []codexAppSummary }
	var state struct{ Apps []codexInstalledApp }
	if err := json.Unmarshal(apps, &list); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(installed, &state); err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, app := range list.Data {
		names[app.ID] = app.Name
	}
	result := []model.Integration{}
	for _, app := range state.Apps {
		result = append(result, model.Integration{Provider: provider, Ref: codexAppPrefix + app.ID, Name: names[app.ID], Kind: "app", Enabled: app.Enabled, Callable: app.Callable})
	}
	return result, nil
}

func (s *providerSession) discoverCodexIntegrations(thread string) ([]model.Integration, error) {
	catalog, err := s.codexAppConnections(thread)
	if err != nil {
		return nil, err
	}
	if err := s.discoverCodexAppTools(thread, catalog); err != nil {
		return nil, err
	}
	servers, err := codexCatalogPages[codexMCPStatus](s, thread, "mcpServerStatus/list", "toolsAndAuthOnly", "Codex MCP discovery")
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		// codex_apps transports app:// connections; app metadata owns its tools.
		if server.Name == "codex_apps" {
			continue
		}
		item := server.integration(s.input.ProviderID)
		s.recordCodexMCPPolicy(item.Ref, server)
		catalog = append(catalog, item)
	}
	return catalog, nil
}

func (s *providerSession) codexAppConnections(thread string) ([]model.Integration, error) {
	apps, err := codexCatalogPages[codexAppSummary](s, thread, "app/list", "", "Codex app discovery")
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(struct {
		Data []codexAppSummary `json:"data"`
	}{apps})
	var installed json.RawMessage
	if err := s.rpc.Call(s.ctx, "app/installed", map[string]any{codexCatalogThreadID: thread, "forceRefresh": true}, &installed); err != nil {
		return nil, fmt.Errorf("Codex callable state: %w", err)
	}
	return codexAppCatalog(s.input.ProviderID, raw, installed)
}

func codexCatalogPages[T any](s *providerSession, thread, method, detail, label string) ([]T, error) {
	items := []T{}
	cursor := ""
	seen := map[string]bool{}
	for {
		var page codexCatalogPage[T]
		if err := s.rpc.Call(s.ctx, method, codexCatalogParams(thread, cursor, detail), &page); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		items = append(items, page.Data...)
		next, err := codexCatalogCursor(page.NextCursor, seen)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if next == "" {
			return items, nil
		}
		cursor = next
	}
}

func codexCatalogParams(thread, cursor, detail string) map[string]any {
	params := map[string]any{codexCatalogThreadID: thread, "limit": codexCatalogBatchSize}
	if cursor != "" {
		params["cursor"] = cursor
	}
	if detail != "" {
		params["detail"] = detail
	}
	return params
}

func codexCatalogCursor(next *string, seen map[string]bool) (string, error) {
	if next == nil || *next == "" {
		return "", nil
	}
	if seen[*next] {
		return "", errors.New("discovery cursor did not advance")
	}
	seen[*next] = true
	return *next, nil
}

func (server codexMCPStatus) integration(provider string) model.Integration {
	item := model.Integration{Provider: provider, Ref: "mcp://" + server.Name, Name: server.Name, Kind: "mcp", Enabled: server.RuntimeStatus == nil || *server.RuntimeStatus != "disabled", Callable: server.callable()}
	if server.ToolsError != nil {
		item.Error = "native MCP tool discovery failed; reconnect and refresh"
	} else if !item.Callable && item.Enabled {
		item.Error = "native MCP connection is not ready; reconnect and refresh"
	}
	for name, tool := range server.Tools {
		item.Tools = append(item.Tools, model.IntegrationTool{Name: name, Description: tool.Description, ReadOnly: integrationToolReadOnly(name, tool.Annotations)})
	}
	slices.SortFunc(item.Tools, func(a, b model.IntegrationTool) int { return strings.Compare(a.Name, b.Name) })
	return item
}

func (server codexMCPStatus) callable() bool {
	connected := server.RuntimeStatus == nil || *server.RuntimeStatus == "connected"
	return server.ToolsError == nil && len(server.Tools) > 0 && connected
}

func (s *providerSession) recordCodexMCPPolicy(ref string, server codexMCPStatus) {
	if server.PluginID == nil || *server.PluginID == "" {
		return
	}
	if s.nativePolicies == nil {
		s.nativePolicies = map[string]string{}
	}
	s.nativePolicies[ref] = nativeConfigKey("plugins", *server.PluginID, "mcp_servers", server.Name)
}

func (s *providerSession) discoverCodexAppTools(thread string, catalog []model.Integration) error {
	for start := 0; start < len(catalog); start += codexCatalogBatchSize {
		batch := catalog[start:min(start+codexCatalogBatchSize, len(catalog))]
		var response codexAppReadResponse
		params := map[string]any{codexCatalogThreadID: thread, "appIds": codexAppIDs(batch), "includeTools": true}
		if err := s.rpc.Call(s.ctx, "app/read", params, &response); err != nil {
			return fmt.Errorf("Codex app metadata unavailable: %w", err)
		}
		response.apply(batch)
	}
	return nil
}

func codexAppIDs(batch []model.Integration) []string {
	ids := make([]string, 0, len(batch))
	for _, item := range batch {
		ids = append(ids, strings.TrimPrefix(item.Ref, codexAppPrefix))
	}
	return ids
}

func (response codexAppReadResponse) apply(batch []model.Integration) {
	apps := response.byID()
	for i := range batch {
		app, found := apps[strings.TrimPrefix(batch[i].Ref, codexAppPrefix)]
		if !found {
			batch[i].Callable = false
			batch[i].Error = "native app metadata unavailable; reconnect and refresh"
			continue
		}
		app.apply(&batch[i])
	}
}

func (response codexAppReadResponse) byID() map[string]codexAppMetadata {
	apps := make(map[string]codexAppMetadata, len(response.Apps))
	for _, app := range response.Apps {
		prior := apps[app.ID]
		app.ToolSummaries = append(prior.ToolSummaries, app.ToolSummaries...)
		apps[app.ID] = app
	}
	return apps
}

func (app codexAppMetadata) apply(item *model.Integration) {
	item.Name = app.Name
	for _, tool := range app.ToolSummaries {
		if tool.Name != "" {
			item.Tools = append(item.Tools, model.IntegrationTool{Name: tool.Name, Description: tool.Description, ReadOnly: tool.IsEnabled && tool.IsReadOnly})
		}
	}
}
