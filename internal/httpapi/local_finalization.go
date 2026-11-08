package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"whirlwind/internal/model"
	"whirlwind/internal/process"
	"whirlwind/internal/store"
	"whirlwind/internal/workflow"
)

type localFinalizationView struct {
	Task             model.Task           `json:"task"`
	Review           model.Review         `json:"review"`
	Adherence        model.Adherence      `json:"adherence"`
	ApprovedPlanHash string               `json:"approved_plan_hash"`
	PolicyHash       string               `json:"policy_hash"`
	Diff             finalizationDiffPage `json:"diff"`
	Accepted         bool                 `json:"accepted"`
}

type finalizationDiffPage struct {
	Content    string `json:"content"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	HasMore    bool   `json:"has_more"`
}

const finalizationDiffPageBytes = 64 * 1024
const finalizationDiffScanLimit = 64 * 1024 * 1024
const finalizationDiffLineLimit = 256 * 1024

var pinnedGitCommit = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

type boundedOutput struct {
	data     []byte
	limit    int
	overflow bool
}

func (output *boundedOutput) Write(value []byte) (int, error) {
	remaining := output.limit - len(output.data)
	if remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		output.data = append(output.data, value[:remaining]...)
	}
	if remaining < len(value) {
		output.overflow = true
	}
	return len(value), nil
}

func (output *boundedOutput) String() string { return string(output.data) }

func safeGitEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GIT_CONFIG" || strings.HasPrefix(key, "GIT_CONFIG_") || strings.HasPrefix(key, "GIT_OBJECT_") || strings.HasPrefix(key, "GIT_ALTERNATE_") || strings.Contains(" GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_EXTERNAL_DIFF GIT_PAGER GIT_DIFF_OPTS ", " "+key+" ") {
			continue
		}
		result = append(result, entry)
	}
	result = append(result, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0")
	return result
}

func (s *Server) approveLocalFinalization(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.ExpectedVersion < 1 || command.IdempotencyKey == "" {
		return nil, errors.New("local finalization approval requires expected task version and idempotency key")
	}
	if _, err := localID(command.PlanID); err != nil {
		return nil, err
	}
	var approval workflow.LocalFinalizationApproval
	if err := json.Unmarshal(localPayload(command.Payload), &approval); err != nil {
		return nil, err
	}
	if err := validateFinalizationApproval(approval); err != nil {
		return nil, err
	}
	if s.Engine == nil {
		return nil, errors.New("local workflow engine unavailable")
	}
	task, err := s.Engine.ApproveLocalFinalization(ctx, command.PlanID, command.ExpectedVersion, approval, command.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(task)
	return body, err
}

func validateFinalizationApproval(approval workflow.LocalFinalizationApproval) error {
	if approval.PlanVersion < 1 || approval.ApprovedPlanHash == "" || approval.ReviewedCommit == "" || approval.ReviewID == "" || approval.VerificationID == "" || approval.PolicyHash == "" {
		return errors.New("local finalization approval requires the exact approved plan, candidate, review, verification, and policy identities")
	}
	return nil
}

func (s *Server) inspectFinalization(ctx context.Context, command LocalCommand) (json.RawMessage, error) {
	if command.DiffOffset < 0 || command.DiffOffset > finalizationDiffScanLimit {
		return nil, errors.New("finalization diff offset is outside the supported range")
	}
	record, err := s.Store.Get(ctx, "tasks", command.PlanID)
	if err != nil {
		return nil, err
	}
	task, err := store.Decode[model.Task](record)
	if err != nil {
		return nil, err
	}
	review, adherence, accepted, err := s.Engine.LoadAcceptedReviewReport(ctx, command.PlanID)
	if err != nil {
		return nil, err
	}
	project, err := s.Engine.Project(ctx, task.ProjectID)
	if err != nil {
		return nil, err
	}
	view := localFinalizationView{Task: task, Review: review, Adherence: adherence, ApprovedPlanHash: approvedPlanHash(task), PolicyHash: project.PolicyHash, Accepted: accepted}
	if task.Stage == "approval" {
		if !accepted {
			return nil, errors.New("critical candidate no longer has an accepted review report")
		}
		view.Diff, err = exactCandidateDiffPage(ctx, task, command.DiffOffset)
		if err != nil {
			return nil, err
		}
	} else if task.Delivery != nil {
		view.Diff, err = pageStoredDiff(task.Delivery.Diff, command.DiffOffset)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(view)
}

func approvedPlanHash(task model.Task) string {
	if task.PlanDocument == nil {
		return ""
	}
	return task.PlanDocument.ApprovedHash
}

func exactCandidateDiffPage(ctx context.Context, task model.Task, offset int64) (finalizationDiffPage, error) {
	if task.Worktree == "" || !pinnedGitCommit.MatchString(task.Base) || !pinnedGitCommit.MatchString(task.ReviewedCommit) {
		return finalizationDiffPage{}, errors.New("critical candidate is missing its worktree or pinned commit identities")
	}
	head, _, err := runBoundedGit(ctx, task.Worktree, finalizationDiffScanLimit, "rev-parse", "HEAD")
	if err != nil {
		return finalizationDiffPage{}, err
	}
	if strings.TrimSpace(head) != task.ReviewedCommit {
		return finalizationDiffPage{}, fmt.Errorf("critical candidate changed after review: expected %s, found %s", task.ReviewedCommit, head)
	}
	status, statusOverflow, err := runBoundedGit(ctx, task.Worktree, finalizationDiffScanLimit, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return finalizationDiffPage{}, err
	}
	if status != "" || statusOverflow {
		return finalizationDiffPage{}, errors.New("critical candidate worktree changed after review; refresh review before local finalization")
	}
	return runCandidateDiffPage(ctx, task, offset)
}

func runCandidateDiffPage(ctx context.Context, task model.Task, offset int64) (finalizationDiffPage, error) {
	output := &diffPageWriter{offset: offset}
	diagnostics := &boundedOutput{limit: 4096}
	command := process.Command(ctx, "git", "-c", "core.pager=cat", "-c", "diff.external=", "diff", "--no-ext-diff", "--no-textconv", "--binary", task.Base+"..."+task.ReviewedCommit)
	command.Dir = task.Worktree
	command.Env = safeGitEnvironment(command.Env)
	command.Stdout, command.Stderr = output, diagnostics
	if err := process.Run(command); err != nil {
		return finalizationDiffPage{}, fmt.Errorf("git candidate diff failed: %w: %s", err, diagnostics.String())
	}
	return output.page()
}

type diffPageWriter struct {
	line        []byte
	content     []byte
	offset      int64
	total       int64
	nextOffset  int64
	pageFull    bool
	hasMore     bool
	overflow    bool
	lineTooLong bool
}

func (writer *diffPageWriter) Write(value []byte) (int, error) {
	for _, char := range value {
		position := writer.total
		writer.total++
		if writer.total > finalizationDiffScanLimit {
			writer.overflow = true
			continue
		}
		if position < writer.offset {
			continue
		}
		if writer.pageFull {
			writer.hasMore = true
			continue
		}
		writer.line = append(writer.line, char)
		if len(writer.line) > finalizationDiffLineLimit {
			writer.lineTooLong = true
			writer.line = nil
			writer.pageFull = true
			writer.hasMore = true
			continue
		}
		if char == '\n' {
			writer.appendLine()
		}
	}
	return len(value), nil
}

func (writer *diffPageWriter) appendLine() {
	if len(writer.content) > 0 && len(writer.content)+len(writer.line) > finalizationDiffPageBytes {
		writer.pageFull = true
		writer.hasMore = true
		writer.line = nil
		return
	}
	writer.content = append(writer.content, writer.line...)
	writer.line = nil
	writer.nextOffset = writer.total
	if len(writer.content) >= finalizationDiffPageBytes {
		writer.pageFull = true
	}
}

func (writer *diffPageWriter) page() (finalizationDiffPage, error) {
	if writer.overflow {
		return finalizationDiffPage{}, fmt.Errorf("exact candidate diff exceeds the %d-byte scan limit", finalizationDiffScanLimit)
	}
	if writer.lineTooLong {
		return finalizationDiffPage{}, fmt.Errorf("exact candidate diff contains a line longer than %d bytes", finalizationDiffLineLimit)
	}
	if writer.offset > writer.total {
		return finalizationDiffPage{}, errors.New("finalization diff offset is past the end of the exact candidate diff")
	}
	if len(writer.line) > 0 && !writer.pageFull {
		if len(writer.content) > 0 && len(writer.content)+len(writer.line) > finalizationDiffPageBytes {
			writer.hasMore = true
		} else {
			writer.content = append(writer.content, writer.line...)
			writer.nextOffset = writer.total
		}
	}
	if writer.nextOffset == 0 {
		writer.nextOffset = writer.offset
	}
	return finalizationDiffPage{Content: string(writer.content), Offset: writer.offset, NextOffset: writer.nextOffset, HasMore: writer.hasMore}, nil
}

func pageStoredDiff(diff string, offset int64) (finalizationDiffPage, error) {
	writer := &diffPageWriter{offset: offset}
	_, _ = writer.Write([]byte(diff))
	return writer.page()
}

func runBoundedGit(ctx context.Context, worktree string, limit int, args ...string) (string, bool, error) {
	command := process.Command(ctx, "git", args...)
	command.Dir = worktree
	command.Env = safeGitEnvironment(command.Env)
	output := &boundedOutput{limit: limit}
	diagnostics := &boundedOutput{limit: 4096}
	command.Stdout, command.Stderr = output, diagnostics
	if err := process.Run(command); err != nil {
		return "", false, fmt.Errorf("git %v failed: %w: %s", args, err, diagnostics.String())
	}
	return output.String(), output.overflow, nil
}
