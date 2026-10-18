-- +goose Up
-- Rewrite projects.registered_at, projects.archived_at and
-- workspace_repos.registered_at values that a local-zone clock left in the
-- column. The driver stores a time.Time by its String() form, so a value written
-- from time.Now() (rather than time.Now().UTC()) kept both its zone and its
-- monotonic reading:
--
--   2026-10-18 05:03:55.6144103 +0330 +0330 m=+185.748497701
--
-- against the canonical form every other writer produces:
--
--   2026-10-18 01:33:55.6144103 +0000 UTC
--
-- The reader parses these columns with "2006-01-02 15:04:05.999999999 -0700
-- MST". A zone whose Go name is alphabetic ("+0800 CST", "+0000 UTC") matches,
-- but one whose name is a numeric offset does not: Asia/Tehran renders as
-- "+0330", so the MST element is handed "+0330" and the parse fails. The driver
-- then returns the column as a string, every scan into time.Time fails, and the
-- project becomes unreadable the moment it is registered:
--
--   GET /api/v1/projects -> 500 PROJECTS_LIST_FAILED "Failed to load projects"
--
-- which on a fresh install takes out the projects list and the whole first-run
-- flow. The writers are fixed in the same change; this repairs rows that a
-- released build already wrote.
--
-- Two shape details this has to respect, copied from 0120 which did the same for
-- sessions.activity_last_at:
--  * The offset is found by search, not at a fixed column: Go trims trailing
--    zeros from the fractional second, so ".42722 +0800" and ".349363 +0800"
--    place the sign differently.
--  * The fractional digits are carried over verbatim. strftime's %f renders
--    milliseconds only, which would silently truncate microsecond precision.

-- +goose StatementBegin
UPDATE projects
SET registered_at =
    strftime('%Y-%m-%d %H:%M:%S',
        substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1),
        (CASE substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 1, 1)
            WHEN '+' THEN '-' ELSE '+' END)
            || substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 2, 2) || ' hours',
        (CASE substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 1, 1)
            WHEN '+' THEN '-' ELSE '+' END)
            || substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 4, 2) || ' minutes'
    )
    || CASE
        WHEN instr(substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1), '.') = 0
        THEN ''
        ELSE substr(
            substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1),
            instr(substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1), '.')
        )
    END
    || ' +0000 UTC'
-- Matched by "not already UTC" rather than by the monotonic suffix: a
-- local-zone value breaks the reader whether or not it carried a monotonic
-- reading, and both shapes exist in the wild. The trailing sign test is what
-- keeps values in other shapes - the RFC 3339 the legacy import writes, and the
-- offset form "2026-10-18 05:03:55.6144103+03:30" - out of the rewrite: neither
-- has a sign immediately after a second space, so neither is touched.
WHERE registered_at NOT LIKE '% +0000 UTC'
  AND substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 1, 1) IN ('+', '-')
  AND strftime('%Y-%m-%d %H:%M:%S',
        substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1),
        '+00 hours'
      ) IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE projects
SET archived_at =
    strftime('%Y-%m-%d %H:%M:%S',
        substr(archived_at, 1, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') - 1),
        (CASE substr(archived_at, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') + 1, 1)
            WHEN '+' THEN '-' ELSE '+' END)
            || substr(archived_at, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') + 2, 2) || ' hours',
        (CASE substr(archived_at, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') + 1, 1)
            WHEN '+' THEN '-' ELSE '+' END)
            || substr(archived_at, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') + 4, 2) || ' minutes'
    )
    || CASE
        WHEN instr(substr(archived_at, 1, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') - 1), '.') = 0
        THEN ''
        ELSE substr(
            substr(archived_at, 1, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') - 1),
            instr(substr(archived_at, 1, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') - 1), '.')
        )
    END
    || ' +0000 UTC'
WHERE archived_at IS NOT NULL
  AND archived_at NOT LIKE '% +0000 UTC'
  AND substr(archived_at, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') + 1, 1) IN ('+', '-')
  AND strftime('%Y-%m-%d %H:%M:%S',
        substr(archived_at, 1, instr(archived_at, ' ') + instr(substr(archived_at, instr(archived_at, ' ') + 1), ' ') - 1),
        '+00 hours'
      ) IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE workspace_repos
SET registered_at =
    strftime('%Y-%m-%d %H:%M:%S',
        substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1),
        (CASE substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 1, 1)
            WHEN '+' THEN '-' ELSE '+' END)
            || substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 2, 2) || ' hours',
        (CASE substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 1, 1)
            WHEN '+' THEN '-' ELSE '+' END)
            || substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 4, 2) || ' minutes'
    )
    || CASE
        WHEN instr(substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1), '.') = 0
        THEN ''
        ELSE substr(
            substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1),
            instr(substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1), '.')
        )
    END
    || ' +0000 UTC'
WHERE registered_at NOT LIKE '% +0000 UTC'
  AND substr(registered_at, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') + 1, 1) IN ('+', '-')
  AND strftime('%Y-%m-%d %H:%M:%S',
        substr(registered_at, 1, instr(registered_at, ' ') + instr(substr(registered_at, instr(registered_at, ' ') + 1), ' ') - 1),
        '+00 hours'
      ) IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- Irreversible by design: the original strings carried a monotonic reading that
-- was never meaningful outside the process that wrote it, and the normalized
-- values are what every reader expects.
SELECT 1;
