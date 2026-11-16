package workflow

import (
	"errors"
	"fmt"
	"whirlwind/internal/store"
	"whirlwind/internal/verification"
)

func (run *verificationRun) retainAutofix(manifest verification.JobManifest, index int, result verification.JobResult) (string, error) {
	proposal := result.Autofix
	content, err := verification.ReadAutofixPatch(manifest, index, result)
	if err != nil {
		return "", err
	}
	owner := store.ArtifactOwner{Kind: "verification", ID: run.id}
	kind := "autofix_patch:" + result.Name
	artifact, err := run.engine.Store.CompleteResourceArtifact(run.ctx, owner, kind, proposal.SHA256)
	if errors.Is(err, store.ErrNotFound) {
		// This is a private, exact source artifact, not a log preview. Text
		// redaction would corrupt patch bytes; only metadata enters event logs.
		artifact, err = run.engine.Store.CaptureResource(run.ctx, owner, kind, "application/octet-stream", content, store.CaptureOptions{Limit: store.PatchPayloadLimit})
	}
	if err != nil {
		return "", err
	}
	if artifact.Truncated || artifact.SHA256 != proposal.SHA256 || int64(artifact.StoredBytes) != proposal.Bytes {
		return "", fmt.Errorf("%w: complete exact autofix artifact could not be retained", store.ErrPersistence)
	}
	return artifact.ID, nil
}
