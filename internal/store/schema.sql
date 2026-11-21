CREATE TABLE IF NOT EXISTS resources (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    data TEXT NOT NULL CHECK(json_valid(data)),
    updated_at TEXT NOT NULL,
    PRIMARY KEY(kind, id)
);
CREATE TABLE IF NOT EXISTS projects (
    project_id TEXT PRIMARY KEY NOT NULL,
    project_name TEXT NOT NULL,
    project_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','approved','error','complete')),
    timestamp TEXT NOT NULL
);
-- Registration envelopes own mutable settings. Historical project identity
-- survives unregistration while ideas still reference it.
CREATE TRIGGER IF NOT EXISTS projects_insert AFTER INSERT ON resources WHEN NEW.kind='projects'
BEGIN
    INSERT INTO projects(project_id,project_name,project_path,status,timestamp)
    VALUES(NEW.id,COALESCE(json_extract(NEW.data,'$.name'),''),COALESCE(json_extract(NEW.data,'$.path'),''),COALESCE(json_extract(NEW.data,'$.status'),'draft'),NEW.updated_at)
    ON CONFLICT(project_id) DO UPDATE SET project_name=excluded.project_name,project_path=excluded.project_path,status=excluded.status;
END;
CREATE TRIGGER IF NOT EXISTS projects_update AFTER UPDATE ON resources WHEN NEW.kind='projects'
BEGIN
    UPDATE projects SET project_name=COALESCE(json_extract(NEW.data,'$.name'),''),
        project_path=COALESCE(json_extract(NEW.data,'$.path'),''),
        status=COALESCE(json_extract(NEW.data,'$.status'),status) WHERE project_id=NEW.id;
END;
CREATE TRIGGER IF NOT EXISTS projects_delete AFTER DELETE ON resources WHEN OLD.kind='projects'
BEGIN DELETE FROM projects WHERE project_id=OLD.id AND NOT EXISTS(SELECT 1 FROM ideas WHERE project_id=OLD.id); END;
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS issue_plans (
    repo TEXT NOT NULL,
    issue INTEGER NOT NULL,
    plan_id TEXT NOT NULL UNIQUE,
    plan_kind TEXT NOT NULL DEFAULT 'execution_plans' CHECK(plan_kind = 'execution_plans'),
    PRIMARY KEY(repo, issue),
    FOREIGN KEY(plan_kind, plan_id) REFERENCES resources(kind, id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE IF NOT EXISTS leases (
    id TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    fence INTEGER NOT NULL,
    expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS deliveries (
    id TEXT PRIMARY KEY,
    received_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS idempotency (
    key TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    status INTEGER NOT NULL,
    body BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    attempt_id TEXT NOT NULL,
    attempt_kind TEXT NOT NULL DEFAULT 'sessions' CHECK(attempt_kind IN ('sessions', 'verification')),
    kind TEXT NOT NULL,
    content_type TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    stored_bytes INTEGER NOT NULL,
    sha256 TEXT NOT NULL,
    truncated INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    content BLOB NOT NULL,
    stream_key TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(attempt_kind, attempt_id) REFERENCES resources(kind, id)
);
CREATE INDEX IF NOT EXISTS artifacts_attempt ON artifacts(attempt_id);
CREATE INDEX IF NOT EXISTS artifacts_stream ON artifacts(attempt_id, stream_key);
CREATE TABLE IF NOT EXISTS attempt_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    attempt_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    artifact_id TEXT NOT NULL REFERENCES artifacts(id),
    replay INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(metadata))
);
CREATE INDEX IF NOT EXISTS attempt_events_cursor ON attempt_events(attempt_id, sequence);
CREATE TABLE IF NOT EXISTS proposals (
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    category TEXT NOT NULL,
    problem TEXT NOT NULL,
    mechanism TEXT NOT NULL,
    components TEXT NOT NULL,
    revision TEXT NOT NULL,
    status TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    title TEXT NOT NULL,
    created_at TEXT NOT NULL,
    data TEXT NOT NULL CHECK(json_valid(data)),
    PRIMARY KEY(project_id, id)
);
CREATE INDEX IF NOT EXISTS proposals_project_recent ON proposals(project_id, created_at DESC, id);
CREATE INDEX IF NOT EXISTS proposals_project_category ON proposals(project_id, category, created_at DESC);
CREATE INDEX IF NOT EXISTS proposals_project_status ON proposals(project_id, status, created_at DESC);
CREATE TABLE IF NOT EXISTS proposal_fingerprints (
    project_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    proposal_id TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'published',
    created_at TEXT NOT NULL,
    PRIMARY KEY(project_id, fingerprint)
);
CREATE VIRTUAL TABLE IF NOT EXISTS proposals_fts USING fts5(project_id UNINDEXED, proposal_id UNINDEXED, terms, tokenize='unicode61');

CREATE TABLE IF NOT EXISTS ideation_runs (
    run_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    repository_revision TEXT NOT NULL DEFAULT '',
    configuration_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('queued','running','completed','partial','error','paused')),
    created_at TEXT NOT NULL,
    finished_at TEXT NOT NULL DEFAULT '',
    error_reason TEXT NOT NULL DEFAULT '',
    history_token_limit INTEGER NOT NULL DEFAULT 0 CHECK(history_token_limit>=0),
    history_tokens_used INTEGER NOT NULL DEFAULT 0 CHECK(history_tokens_used>=0 AND history_tokens_used<=history_token_limit),
    UNIQUE(run_id, project_id)
);
CREATE TABLE IF NOT EXISTS ideas (
    idea_id TEXT PRIMARY KEY NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(project_id),
    run_id TEXT NOT NULL,
    idea_title TEXT NOT NULL,
    idea_type TEXT NOT NULL CHECK(idea_type IN ('expand','optimize','harden','simplify')),
    what TEXT NOT NULL,
    why TEXT NOT NULL,
    "where" TEXT NOT NULL,
    how TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('draft','approved','in_process','error','complete')),
    timestamp TEXT NOT NULL,
    FOREIGN KEY(run_id,project_id) REFERENCES ideation_runs(run_id,project_id),
    UNIQUE(idea_id,run_id)
);
CREATE INDEX IF NOT EXISTS ideas_intake ON ideas(status,timestamp);
-- Novelty comparisons are deferred, so historical fingerprints cannot block idea records.
DROP INDEX IF EXISTS ideas_scope_fingerprint;
CREATE TABLE IF NOT EXISTS ideation_directions (
    run_id TEXT NOT NULL REFERENCES ideation_runs(run_id),
    direction TEXT NOT NULL CHECK(direction IN ('expand','optimize','harden','simplify')),
    candidate_count INTEGER NOT NULL DEFAULT 0 CHECK(candidate_count BETWEEN 0 AND 5),
    status TEXT NOT NULL CHECK(status IN ('queued','running','completed','error')),
    idea_id TEXT REFERENCES ideas(idea_id),
    error_reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(run_id,direction)
);
CREATE TABLE IF NOT EXISTS idea_candidates (
    candidate_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES ideation_runs(run_id),
    direction TEXT NOT NULL CHECK(direction IN ('expand','optimize','harden','simplify')),
    ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 1 AND 5),
    status TEXT NOT NULL CHECK(status IN ('generating','generated','accepted','rejected','error')),
    fingerprint TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(content)),
    reason TEXT NOT NULL DEFAULT '',
    UNIQUE(run_id,direction,ordinal)
);
CREATE TABLE IF NOT EXISTS planner_runs (
    planner_run_id TEXT PRIMARY KEY,
    attempt_kind TEXT NOT NULL DEFAULT 'sessions' CHECK(attempt_kind='sessions'),
    idea_id TEXT NOT NULL,
    idea_run_id TEXT NOT NULL REFERENCES ideation_runs(run_id),
    timestamp TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('in_process','completed','error')),
    markdown_path TEXT,
    plan_id TEXT REFERENCES plans(plan_id) DEFERRABLE INITIALLY DEFERRED,
    finished_at TEXT NOT NULL DEFAULT '',
    error_reason TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(attempt_kind,planner_run_id) REFERENCES resources(kind,id) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY(idea_id,idea_run_id) REFERENCES ideas(idea_id,run_id),
    CHECK(status <> 'completed' OR (markdown_path IS NOT NULL AND length(trim(markdown_path))>0 AND plan_id IS NOT NULL AND length(finished_at)>0))
);
CREATE UNIQUE INDEX IF NOT EXISTS planner_runs_active_idea ON planner_runs(idea_id) WHERE status='in_process';

-- Physical plan registries share the resource transaction, event cursor and
-- optimistic version. No old workflow data is imported into these tables.
CREATE TABLE IF NOT EXISTS plans (
    plan_id TEXT PRIMARY KEY NOT NULL,
    idea_id TEXT NOT NULL UNIQUE REFERENCES ideas(idea_id),
    plan_path TEXT NOT NULL CHECK(length(trim(plan_path))>0 AND substr(plan_path,1,1)='/'),
    status TEXT NOT NULL CHECK(status IN ('draft','approved','error','complete')),
    timestamp TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS plans_dispatch ON plans(status,timestamp);
CREATE TRIGGER IF NOT EXISTS plans_linked_delete BEFORE DELETE ON plans
WHEN EXISTS(SELECT 1 FROM resources WHERE kind='ideas' AND json_extract(data,'$.plan_id')=OLD.plan_id)
BEGIN SELECT RAISE(ABORT,'linked idea plan history cannot be deleted'); END;

CREATE TABLE IF NOT EXISTS plan_versions (
    version_id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    plan_kind TEXT NOT NULL DEFAULT 'execution_plans' CHECK(plan_kind='execution_plans'),
    plan_version INTEGER NOT NULL CHECK(plan_version > 0),
    specification TEXT NOT NULL,
    document_hash TEXT NOT NULL DEFAULT '',
    configuration_hash TEXT NOT NULL DEFAULT '',
    requirements TEXT NOT NULL CHECK(json_valid(requirements)),
    configuration_references TEXT NOT NULL CHECK(json_valid(configuration_references)),
    data TEXT NOT NULL CHECK(json_valid(data)),
    created_at TEXT NOT NULL,
    UNIQUE(plan_id,plan_version),
    FOREIGN KEY(plan_kind,plan_id) REFERENCES resources(kind,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER IF NOT EXISTS plan_versions_immutable_update BEFORE UPDATE ON plan_versions
BEGIN SELECT RAISE(ABORT,'approved plan versions are immutable'); END;
CREATE TRIGGER IF NOT EXISTS plan_versions_immutable_delete BEFORE DELETE ON plan_versions
BEGIN SELECT RAISE(ABORT,'approved plan versions are immutable'); END;
-- Component lookup uses proposals.components JSON; discard the derived copy.
DROP TABLE IF EXISTS proposal_components;
-- Current layouts are installed directly; old migration history is unused.
DROP TABLE IF EXISTS migrations;

-- Lightweight delivery/outbox data only; business state remains in resources.
CREATE TABLE IF NOT EXISTS role_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    role TEXT NOT NULL CHECK (role IN ('planner_tasks','coding_tasks','reviewer_tasks','finalizer_tasks')),
    plan_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    stage TEXT NOT NULL,
    ready INTEGER NOT NULL DEFAULT 0 CHECK (ready IN (0,1)),
    token TEXT NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE (plan_id, version)
);
CREATE INDEX IF NOT EXISTS role_tasks_fifo ON role_tasks(role, id);
CREATE TRIGGER IF NOT EXISTS role_tasks_delete_plan AFTER DELETE ON resources WHEN OLD.kind='execution_plans'
BEGIN DELETE FROM role_tasks WHERE plan_id=OLD.id; END;
