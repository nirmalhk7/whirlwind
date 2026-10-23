package httpapi

import (
	"encoding/json"
	"whirlwind/internal/settingsfile"
)

type settingsFileInput struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
}

func (q apiRequest) settingsFile() error {
	if q.id != "file" {
		return &apiError{404, "route_not_found", "Unsupported route"}
	}
	source, ok := q.Store.Source.(*settingsfile.Source)
	if !ok {
		return &apiError{503, "settings_file_unavailable", "No startup settings file is attached to this process"}
	}
	if q.r.Method == "GET" {
		return q.readSettingsFile(source)
	}
	return q.saveSettingsFile(source)
}

func (q apiRequest) readSettingsFile(source *settingsfile.Source) error {
	if q.r.URL.Query().Get("view") != "file" {
		reply(q.w, 200, source.Snapshot())
		return nil
	}
	file, err := source.File()
	if err != nil {
		return err
	}
	reply(q.w, 200, file)
	return nil
}

func (q apiRequest) saveSettingsFile(source *settingsfile.Source) error {
	var input settingsFileInput
	if err := json.Unmarshal(q.body, &input); err != nil {
		return err
	}
	if input.Revision == "" {
		return &apiError{428, "precondition_required", "File revision required; reopen the file before saving"}
	}
	if err := source.ValidateModeEdit(input.Content); err != nil {
		return &apiError{422, "mode_requires_disk_edit", err.Error()}
	}
	if err := settingsfile.ValidateFile(input.Content); err != nil {
		return err
	}
	reply(q.w, 200, map[string]bool{"saved": true})
	return nil
}
