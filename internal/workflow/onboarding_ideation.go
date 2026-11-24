package workflow

import (
	"context"
	"errors"

	"whirlwind/internal/model"
	"whirlwind/internal/store"
)

func (e *Engine) InitialIdeationEnabled(ctx context.Context) bool {
	return e.settings(ctx).Ideation.OnRegistrationEnabled()
}

type onboardingIdeationAdmission struct {
	project model.Project
	guards  []store.Change
	hold    string
}

// Re-read setup and settings at the leased dispatch boundary; a project or
// setting change before the running transition rejects the whole write.
func (e *Engine) onboardingIdeationAdmission(ctx context.Context, projectID string) (onboardingIdeationAdmission, error) {
	var admission onboardingIdeationAdmission
	projectRecord, err := e.Store.Get(ctx, "projects", projectID)
	if err != nil {
		return admission, err
	}
	admission.project, err = store.Decode[model.Project](projectRecord)
	if err != nil {
		return admission, err
	}
	admission.guards = append(admission.guards, store.VersionPrecondition("projects", projectID, projectRecord.Version))
	settings := DefaultSettings()
	settingsRecord, err := e.Store.Get(ctx, "settings", "global")
	if err == nil {
		settings, err = store.Decode[Settings](settingsRecord)
		if err != nil {
			return admission, err
		}
		admission.guards = append(admission.guards, store.VersionPrecondition("settings", "global", settingsRecord.Version))
	} else if !errors.Is(err, store.ErrNotFound) {
		return admission, err
	}
	switch {
	case !settings.Ideation.OnRegistrationEnabled():
		admission.hold = "automatic ideation is disabled by workers.ideation.on_registration"
	case settings.Stopped || admission.project.Paused:
		admission.hold = "project or runners are paused"
	case admission.project.Status == "error":
		admission.hold = "project setup failed; retry setup or start ideation manually"
	case admission.project.Status != "" && admission.project.Status != "approved":
		admission.hold = "waiting for project setup to finish"
	}
	return admission, nil
}
