-- +goose Up

-- Whether a sandbox counts against its organization's plan, decided when its
-- session is created: Freestyle always; Coder unless the organization holds the
-- coder capability (WorkOS metadata capabilities=coder) or the session is a
-- worker spawned by an unbilled parent. The capability lives in the caller's
-- sign-in, which background billing work (usage sampling, over-limit pauses)
-- does not have, so it is recorded here. Billing reads "Freestyle OR billed",
-- so Freestyle sandboxes created before this column stay billed with no
-- backfill. Numbered 00082 and idempotent: 00081 belongs to the GitHub
-- installation scope change (PR #6587), already applied on staging.
ALTER TABLE ao_sandboxes ADD COLUMN IF NOT EXISTS billed BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE ao_sandboxes DROP COLUMN IF EXISTS billed;
