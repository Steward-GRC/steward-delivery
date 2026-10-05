-- Copyright 2026 The Steward Authors
-- SPDX-License-Identifier: Apache-2.0

-- magic_link_tokens holds the read-only share links. The token is the bearer
-- secret the link carries.
CREATE TABLE magic_link_tokens (
    token              TEXT        PRIMARY KEY,
    policy_version_id  TEXT        NOT NULL,
    created_by_user_id TEXT        NOT NULL,
    sensitive          BOOLEAN     NOT NULL DEFAULT FALSE,
    expires_at         TIMESTAMPTZ NOT NULL,
    revoked_at         TIMESTAMPTZ,
    revoked_by_user_id TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_mlt_policy_version ON magic_link_tokens (policy_version_id);
CREATE INDEX idx_mlt_expires        ON magic_link_tokens (expires_at) WHERE revoked_at IS NULL;

-- pdf_jobs projects each PdfRender resource: the export request inserts the
-- row as 'pending' before it creates the resource, and the informer moves it
-- on as the renderer reports. The status set is enforced in the store, not by
-- a CHECK constraint, so it can grow without a schema change.
CREATE TABLE pdf_jobs (
    job_id            TEXT        PRIMARY KEY,
    policy_version_id TEXT        NOT NULL,
    requester_user_id TEXT        NOT NULL,
    sensitive         BOOLEAN     NOT NULL DEFAULT FALSE,
    status            TEXT        NOT NULL DEFAULT 'pending',  -- pending | processing | done | failed
    artifact_key      TEXT,
    error_msg         TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at      TIMESTAMPTZ
);

CREATE INDEX idx_pdfjobs_policy_version ON pdf_jobs (policy_version_id);
