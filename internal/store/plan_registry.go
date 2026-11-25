package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"whirlwind/internal/model"
)

func syncExecutionPlanTx(ctx context.Context, tx *sql.Tx, id string, data []byte) error {
	var plan model.ExecutionPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	// Validate source identity even before the core row can be published.
	if plan.IdeaID == "" {
		return nil
	}
	idea, _, err := txResource[model.Idea](ctx, tx, "ideas", plan.IdeaID)
	if err != nil {
		return err
	}
	if idea.ProjectID != plan.ProjectID || idea.RunID != plan.IdeationRunID {
		return ErrConflict
	}
	// Attempts and pre-publication execution state live in resources.
	if plan.PlanDocument == nil || plan.PlanDocument.Path == "" || plan.PlanDocument.Hash == "" {
		return nil
	}
	var at string
	if err := tx.QueryRowContext(ctx, "SELECT updated_at FROM resources WHERE kind='execution_plans' AND id=?", id).Scan(&at); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plans(plan_id,idea_id,plan_path,status,timestamp) VALUES(?,?,?,?,?)
	 ON CONFLICT(plan_id) DO UPDATE SET plan_path=excluded.plan_path,status=excluded.status`, id, plan.IdeaID, plan.PlanDocument.Path, planSQLStatus(plan), at)
	return err
}

func planSQLStatus(plan model.ExecutionPlan) string {
	if plan.Stage == "complete" {
		return "complete"
	}
	if plan.Wait == nil && (plan.Stage == "paused" || plan.Stage == "cancelled") && plan.Error != "" && plan.Error != model.GlobalRunnerStopReason {
		return "error"
	}
	if doc := plan.PlanDocument; doc != nil && doc.ApprovedHash != "" && doc.ApprovedHash == doc.Hash {
		return "approved"
	}
	return "draft"
}

func syncPlanVersionTx(ctx context.Context, tx *sql.Tx, id string, data []byte) error {
	cut := strings.LastIndexByte(id, ':')
	if cut <= 0 {
		return fmt.Errorf("%w: approved plan version identity is invalid", ErrConflict)
	}
	version, err := strconv.Atoi(id[cut+1:])
	if err != nil || version <= 0 {
		return fmt.Errorf("%w: approved plan version identity is invalid", ErrConflict)
	}
	var plan model.PlanSpecification
	if err := json.Unmarshal(data, &plan); err != nil {
		return err
	}
	hash, configuration := "", ""
	var references []model.ConfigurationReference
	if plan.Approval != nil {
		if plan.Approval.PlanVersion != version {
			return ErrConflict
		}
		hash, configuration, references = plan.Approval.DocumentHash, plan.Approval.ConfigurationHash, plan.Approval.References
	}
	requirements, err := json.Marshal(plan.Requirements)
	if err != nil {
		return err
	}
	refs, err := json.Marshal(references)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plan_versions(version_id,plan_id,plan_version,specification,document_hash,configuration_hash,requirements,configuration_references,data,created_at)
	SELECT ?,?,?,?,?,?,?,?,?,updated_at FROM resources WHERE kind='plan_versions' AND id=?`, id, id[:cut], version, plan.Specification, hash, configuration, string(requirements), string(refs), string(data), id)
	return err
}
