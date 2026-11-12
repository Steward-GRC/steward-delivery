-- Copyright 2026 The Steward Authors
-- SPDX-License-Identifier: Apache-2.0

-- The version-history entries an export asked to print after the policy. NULL
-- means the export did not ask for the page; an empty array means it did and
-- nothing is recorded. The gateway sends the entries with the request.
ALTER TABLE pdf_jobs ADD COLUMN history JSONB;
