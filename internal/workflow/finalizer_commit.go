package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"whirlwind/internal/model"
	"whirlwind/internal/process"
)

// Preserve accepted attribution/timestamps and create a deterministic delivery
// commit with supervisor-authored provenance. No hooks, signing, worktree edits,
// caller-supplied Git arguments, or agent .git write grants are involved.
func createFinalizationCommit(ctx context.Context, source string, intent model.LocalFinalization) (string, error) {
	raw, err := submissionGitRun(ctx, source, "cat-file", "commit", intent.ReviewedCommit)
	if err != nil {
		return "", err
	}
	headers, _, _ := strings.Cut(raw, "\n\n")
	var author, committer string
	for _, line := range strings.Split(headers, "\n") {
		if value, ok := strings.CutPrefix(line, "author "); ok {
			author = value
		}
		if value, ok := strings.CutPrefix(line, "committer "); ok {
			committer = value
		}
	}
	if author == "" || committer == "" {
		return "", errors.New("accepted commit lacks valid attribution")
	}
	message := fmt.Sprintf("Whirlwind local delivery\n\nPlan: %s\nPlan-Version: %d\nApproved-Document: %s\nConfiguration: %s\nAccepted-Commit: %s\nVerification: %s\nReview: %s\nPolicy: %s\n", intent.PlanID, intent.PlanVersion, intent.DocumentHash, intent.ConfigurationHash, intent.ReviewedCommit, intent.VerificationID, intent.ReviewID, intent.PolicyHash)
	object := "tree " + intent.Tree + "\nparent " + intent.Base + "\nauthor " + author + "\ncommitter " + committer + "\n\n" + message
	cmd := process.Command(ctx, "git", "--literal-pathspecs", "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Dir = source
	cmd.Stdin = strings.NewReader(object)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := process.Run(cmd); err != nil {
		return "", fmt.Errorf("create exact-tree delivery commit: %w: %.4000s", err, stderr.Bytes())
	}
	return strings.TrimSpace(stdout.String()), nil
}
