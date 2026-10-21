package api

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/config"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/model"
)

// getRegistry returns the user-safe Registry projection for a workspace.
func (s *Service) getRegistry(w http.ResponseWriter, r *http.Request) {
	workspace, ok := s.requestWorkspace(r)
	if !ok {
		problem(w, http.StatusNotFound, "workspace_not_found", "Workspace not found")
		return
	}
	registry, err := s.registry(r.Context(), workspace)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "registry_unavailable", "Could not read the platform Registry")
		return
	}
	writeJSON(w, http.StatusOK, registry)
}

// registry loads a Registry and applies required platform policy.
func (s *Service) registry(ctx context.Context, workspace config.Workspace) (model.Registry, error) {
	registry, err := s.kube.GetRegistry(ctx, workspace)
	if err != nil {
		return model.Registry{}, err
	}
	required := map[string]bool{}
	for _, name := range workspace.RequiredSkills {
		required[name] = true
	}
	for i := range registry.Skills {
		if registry.Skills[i].Name == "" || registry.Skills[i].Source.Repository == "" {
			return model.Registry{}, errors.New("Registry contains an invalid skill")
		}
		registry.Skills[i].Required = required[registry.Skills[i].Name]
		delete(required, registry.Skills[i].Name)
	}
	if len(required) != 0 {
		return model.Registry{}, errors.New("Registry is missing a required skill")
	}
	for _, plugin := range registry.Plugins {
		if plugin.Name == "" || plugin.Plugin.Name == "" {
			return model.Registry{}, errors.New("Registry contains an invalid plugin")
		}
	}
	return registry, nil
}

// resolveRegistrySelection resolves public Registry identifiers into internal runtime specifications.
func resolveRegistrySelection(workspace config.Workspace, registry model.Registry, value *model.AgentConfig) bool {
	wantedSkills := map[string]bool{}
	for _, name := range workspace.RequiredSkills {
		wantedSkills[name] = true
	}
	for _, name := range value.SelectedSkills {
		wantedSkills[name] = true
	}
	wantedPlugins := map[string]bool{}
	for _, name := range value.SelectedPlugins {
		wantedPlugins[name] = true
	}
	if len(wantedSkills) > 50 || len(wantedPlugins) > 50 {
		return false
	}
	value.SelectedSkills, value.SelectedPlugins = nil, nil
	value.SkillSources = append([]model.SkillSource(nil), value.CustomSkillSources...)
	value.PluginSpecs = nil
	sourceNames := map[string]bool{}
	for _, source := range value.CustomSkillSources {
		sourceNames[source.Name] = true
	}
	for _, skill := range registry.Skills {
		if wantedSkills[skill.Name] {
			if sourceNames[skill.Source.Name] {
				return false
			}
			sourceNames[skill.Source.Name] = true
			value.SelectedSkills = append(value.SelectedSkills, skill.Name)
			value.SkillSources = append(value.SkillSources, skill.Source)
			delete(wantedSkills, skill.Name)
		}
	}
	for _, plugin := range registry.Plugins {
		if wantedPlugins[plugin.Name] {
			value.SelectedPlugins = append(value.SelectedPlugins, plugin.Name)
			value.PluginSpecs = append(value.PluginSpecs, plugin.Plugin)
			delete(wantedPlugins, plugin.Name)
		}
	}
	sort.Strings(value.SelectedSkills)
	sort.Strings(value.SelectedPlugins)
	return len(wantedSkills) == 0 && len(wantedPlugins) == 0
}
