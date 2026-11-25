package verification

import "errors"

const ProjectSetupPurpose = "project_setup"

// Setup uses the existing project operation identity, before a plan exists.
// Empty purpose retains the approved-plan contract of existing manifests.
func (m JobManifest) validateAuthority() error {
	if m.ConfigurationHash == "" {
		return errors.New("verification manifest requires approved configuration identity")
	}
	switch m.Purpose {
	case "":
		if m.PlanID == "" || m.PlanVersion < 1 || m.PlanHash == "" || (m.ProjectID != "" && m.OfflineDockerImage == "") || m.OperationID != "" {
			return errors.New("verification manifest requires approved plan and configuration identity")
		}
	case ProjectSetupPurpose:
		if !jobIdentifier.MatchString(m.ProjectID) || !jobIdentifier.MatchString(m.OperationID) || m.OperationID != m.JobID || m.PlanID != "" || m.PlanVersion != 0 || m.PlanHash != "" {
			return errors.New("project setup requires project and operation identity without plan approval")
		}
	default:
		return errors.New("unsupported verification job purpose")
	}
	return nil
}
