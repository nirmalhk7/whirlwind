CREATE TABLE IF NOT EXISTS resources (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    version INTEGER NOT NULL CHECK(version > 0),
    data TEXT NOT NULL CHECK(json_valid(data)),
    updated_at TEXT NOT NULL,
    PRIMARY KEY(kind, id)
);
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS issue_tasks (
    repo TEXT NOT NULL,
    issue INTEGER NOT NULL,
    task_id TEXT NOT NULL UNIQUE,
    task_kind TEXT NOT NULL DEFAULT 'tasks' CHECK(task_kind = 'tasks'),
    PRIMARY KEY(repo, issue),
    FOREIGN KEY(task_kind, task_id) REFERENCES resources(kind, id) DEFERRABLE INITIALLY DEFERRED
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
CREATE TABLE IF NOT EXISTS proposal_components (
    project_id TEXT NOT NULL,
    component TEXT NOT NULL,
    proposal_id TEXT NOT NULL,
    PRIMARY KEY(project_id, component, proposal_id),
    FOREIGN KEY(project_id, proposal_id) REFERENCES proposals(project_id, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS proposal_components_lookup ON proposal_components(project_id, component, proposal_id);
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
