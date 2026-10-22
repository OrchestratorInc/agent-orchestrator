# Contributor Project

This package feeds a private GitHub Project for core contributors working on open
issues in `OrchestratorInc/agent-orchestrator`. Areas and owners are suggestions.
The proposed core list is editable in `data/core-contributors.json`.

## How the Project is fed

The workflow runs when issues are opened or reopened, hourly at
minute 17, and manually. Sync is skipped until `AO_CONTRIBUTOR_PROJECT_URL` is
set. Pull requests touching this package or the workflow run the Python tests
with read-only repository permissions. Sync reads trusted default-branch code.

It adds missing open issues and fills blank `Suggested area` and `Suggested
owner` fields. Missing fields and area options are added. Option updates preserve
existing IDs, names, colors and descriptions. Archived items and human values
are preserved. Every item gets a field/archive recheck before each fill,
including items added in the same run: when the Project item list lags, adding
an issue returns the item that is already there. The API cannot make a recheck
and write atomic.

It never changes assignees, labels, status, contributor approval or archive
choices. An owner suggestion does not establish capacity or consent. Nonblank
suggestions are never replaced, even after maps change. Area evidence is not
created or filled. Issue-specific routing reasons remain in the reviewed map.

`data/issue-areas.json` routes issues into 28 areas. Unknown issues and reports
about landing code moved out of this repo go to `needs-area`.
`data/area-owners.json` supplies up to three core logins per area. `shared-infra`,
`needs-area` and unowned areas leave the owner blank. New routes need body review.

## Setup

1. Select a private GitHub Project and set repository variable
   `AO_CONTRIBUTOR_PROJECT_URL` to its full URL.
2. Install a GitHub App with organization Projects read/write and repository
   Issues read access. Set variable `AO_CONTRIBUTOR_APP_CLIENT_ID` and secret
   `AO_CONTRIBUTOR_APP_PRIVATE_KEY`. Once the URL is set, missing credentials fail
   the run. The repository's default `GITHUB_TOKEN` cannot access org Projects.
3. Manual runs can use `GH_TOKEN` with Projects write and issue read access.
   A classic token needs `project` scope. Authenticate the GitHub command line.

See [GitHub's setup guide](https://docs.github.com/en/issues/planning-and-tracking-with-projects/automating-your-project/automating-projects-using-actions).
The scripts use Python's standard library and GitHub command-line access.
From `.github/scripts/contributor-project/`, inspect a plan before applying it:

```sh
python3 scripts/sync_project.py --project-url "$PROJECT_URL"
# Add --apply after reviewing the plan. The default is read-only.
```

HTTP 429 responses, rate-limited HTTP 403 responses and GraphQL `RATE_LIMITED`
errors retry once after `Retry-After`, or 60 seconds if absent. Other HTTP 403
errors fail immediately. Timeouts, connection failures, HTTP 5xx and GraphQL
internal errors get their own single retry after 5 seconds, then fail. If apply
is still rate limited after the retry, it logs completed operations, prints a
partial summary and exits successfully. The next run resumes blanks. The first
backfill may also need more than one run due to the workflow timeout.

## Refresh owners and routes

From `.github/scripts/contributor-project/` in a source checkout:

```sh
git fetch origin main
python3 scripts/ownership_extract.py --repo ../../..
python3 scripts/build_owners.py
python3 scripts/check_areas.py --repo ../../.. --ref origin/main
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests
```

Extraction reads public merged PRs and blame at `origin/main` without changing
source files. Delete `research/ownership-cache/graphql-*.json` before extraction
to refresh cached PRs. Raw history and caches stay in ignored `research/`.
Only data maps belong in git. Review core membership before publishing changes.

The core rule is at least 10 merged PRs in the 90 days before capture. This
snapshot selects 27 people. Preserve edited membership with
`python3 scripts/build_owners.py --core-file data/core-contributors.json`.

PR files use the same path exclusions as blame for lockfiles, generated,
vendored, build and snapshot files. PR and review credit follows eligible churn
shares; zero-churn PRs split equally across eligible touched areas. Churn uses
`log1p(total eligible PR lines)`, split by area share, with a 90-day half-life.
Scores use 60% recent PR credit, 25% weighted churn, 10% recent review credit and
5% blame, each divided by its area maximum among core contributors. Reviews use
merge dates because review timestamps are unavailable. Owners need one recent
PR equivalent or two review equivalents in the area. Top core areas use recent
PR credit. The data records the capture time and source commit.

Add a new area to both maps and rebuild owners; the next sync adds its option
while keeping existing selections. [GitHub's option input](https://docs.github.com/en/graphql/reference/projects#projectv2singleselectfieldoptioninput)
supports preserving IDs. Keep closed issue routes; sync reads only open issues.
Every non-fallback path glob must match a tracked path on the checked ref.
Rerun the area check and tests after editing either map.
