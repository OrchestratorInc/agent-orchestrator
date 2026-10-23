# Contributor Project

This package feeds a private GitHub Project for open issues in
`OrchestratorInc/agent-orchestrator`. Areas and owners are suggestions, not
assignments or a promise of capacity.

## How the Project is fed

The workflow runs hourly at minute 17, on opened, closed and reopened issues,
and manually. It uses trusted default-branch code and skips sync until
`AO_CONTRIBUTOR_PROJECT_URL` is set. Pull requests run offline Python tests.

Sync adds missing open issues, creates missing fields and area options, and
preserves existing option IDs, colors and descriptions. Reviewed routes in
`data/issue-areas.json` always win. Next comes exactly one valid `area:<id>` label,
matched case-insensitively. Unknown IDs, `needs-area` and `shared-infra` are ignored;
multiple valid labels fall through to the classifier. Repository paths match
`data/path-areas.json` in order. A unique path area needs text agreement with
cosine similarity at least 0.20 and a lead of 0.05 over the next area. Otherwise,
TF-IDF text similarity needs a score of 0.30 and a lead of 0.10. The corpus uses
area descriptions, reviewed routing reasons and reviewed titles available in
the same open-issue listing. Uncertain issues go to `needs-area`.

`Suggested owner` contains up to three core logins for the area. `shared-infra`,
`needs-area` and unowned areas leave it blank. The core rule is at least 10
merged PRs in the past 90 days. `data/core-overrides.json` adds or excludes
logins from that list; exclusion wins. Generated core and owner files stay in
ignored `research/owners/`.

CI caches one successful owner build per UTC day. A date-keyed cache restores
the latest owners on later runs. Only scheduled and manual runs rebuild owners;
issue-event runs restore the cache without rebuilding. A failed build emits an
Actions warning and keeps restored owners, or leaves owners blank if none exist.
Sync also warns when the owners file's `generated_at` is more than seven days
old or invalid. Only `steps.owner-build.outcome == 'success'` saves today's cache,
so failures retry on the next scheduled or manual run. The cache uses a `v2`
prefix to bypass any failed attempts saved by the old workflow.
Daily extraction reads public merged PRs, changed paths and reviews. The owner
build reads blame from committed `data/blame-lines.json`. Both steps work with a
shallow checkout. Extraction uses the workflow's `GITHUB_TOKEN`.
Owner builds have a 15-minute timeout; failure still allows sync to continue.

`Sync record` stores the last area and owner text written by the bot as JSON.
Sync can change a suggestion only when it is blank or equals its recorded bot
value. Nonblank values without a record, and values changed by a human, stay.
Before each suggestion write, sync rechecks the field, record and archive state,
including items returned by an add when the item list lagged. Field and record
writes are separate API calls, with the field written first. A crash before
the record write leaves a nonblank value without a matching record. Sync treats
it as human-set, so it needs manual clearing before the bot can manage it again.
Writing the record first would also leave interrupted refreshes looking like
human edits because the old field would differ from the new record.

Closed issues in this repository are archived using the issue state returned
in Project content. Before archiving, the bot writes `"bot_archived": "true"`
in the item's `Sync record`, keeping its recorded suggestions. Every run,
including hourly runs, unarchives OPEN issues with that mark and then clears
the mark. Recovery does not depend on a queued `reopened` event surviving.
If a run stops after recording intent or after unarchiving, the next run can
finish recovery. Open issues archived by a human without that mark stay archived.
Archives made by older bot versions without the mark also stay archived.
Archive writes recheck issue state, repository, archive state and record.
Rechecks and writes are separate API calls, so concurrent human edits can race.
Other repositories, PRs, assignees, labels, status and area evidence are left alone.

## Setup and checks

1. Set repository variable `AO_CONTRIBUTOR_PROJECT_URL` to the private Project URL.
2. Install a GitHub App with organization Projects read/write and repository
   Issues read access. Set variable `AO_CONTRIBUTOR_APP_CLIENT_ID` and secret
   `AO_CONTRIBUTOR_APP_PRIVATE_KEY`. Missing credentials fail configured runs.
   The App token requests only `permission-organization-projects: write` and
   `permission-issues: read`, with `repositories: agent-orchestrator`. Repository
   scope limits issue access while the organization permission allows Project
   access. The pinned action documents these inputs in its
   [action definition](https://github.com/actions/create-github-app-token/blob/64ab729a6294000f5aa4a4bc0b73d1f32bc45510/action.yml)
   and [README](https://github.com/actions/create-github-app-token/blob/64ab729a6294000f5aa4a4bc0b73d1f32bc45510/README.md#inputs).
3. Allow workflow `contents: read`, `issues: read` and `pull-requests: read`.
   A shallow checkout is enough. No extra token or secret is needed.

The scripts use Python's standard library and `gh`. For a local read-only plan,
authenticate `GH_TOKEN` with Project read and issue read access, then run from
`.github/scripts/contributor-project/`:

```sh
python3 scripts/sync_project.py --project-url "$PROJECT_URL"
python3 scripts/sync_project.py --evaluate-classifier
python3 scripts/check_areas.py --repo ../../.. --ref origin/main
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests
```

The default sync is read-only. CI supplies `--apply` for Project changes and
`--owners-file research/owners/area-owners.json`. Classifier evaluation leaves
each reviewed issue out of its training corpus. It uses live title/body when
available, otherwise its routing reason, and skips routes with neither.
Recheck precision and coverage after editing either area map.
Keep each area, glob rule and issue route on one line with
`python3 scripts/format_maps.py`. It checks JSON equality before writing.
The report includes path-only routing for exactly one non-shared area. On the
493 hand-reviewed routes, it assigned 111 and got 53 right: 47.7% precision and
22.5% coverage. This is below the 80% threshold, so unique paths still need text
agreement. This measurement uses 482 live title/body pairs and three routing
reasons. Eight routes have no available text and count as unrouted in path-only
coverage. Coverage among the 485 routes with text is 22.9%.

To rebuild owners locally, delete `research/ownership-cache/` to refresh PR data,
then run `python3 scripts/ownership_extract.py` and
`python3 scripts/build_owners.py`. Raw evidence and caches stay in ignored
`research/`. Extraction fetches PRs merged in the past 540 days, six 90-day
half-lives. A PR older than that window would carry less than 1/64 of its
original churn weight. This keeps the full 90 days needed for core selection
and recent PR/review metrics. Merge-date searches split into smaller windows
before paginating when they exceed GitHub's
[1,000-result search limit](https://docs.github.com/en/graphql/reference/search).
There is no repository-wide PR cap. Incomplete or duplicate pagination fails
the build instead of silently dropping evidence. No all-time metric is emitted.
The cold extraction and owner build took 321.34 seconds after deleting the
cache. No area changed its top owner versus the round 2 build across 28 areas.
Pagination checks use search counts, so they cannot detect omissions in
GitHub's search index. This run omitted [PR #6582](https://github.com/OrchestratorInc/agent-orchestrator/pull/6582)
even though the PR endpoint reported it merged. The omission did not change
any top owner.

Owner scores use weights of 60 for recent PR credit, 25 for weighted churn,
10 for recent review credit and 5 for static blame lines. Each metric is
normalized by its area maximum among core contributors. PR/review credit follows eligible churn
shares. Churn uses `log1p(lines)` with a 90-day
half-life. Owners need one recent PR equivalent or two review equivalents.
Mechanical files are excluded. Reviews use merge dates because review times
are unavailable. A login absent from the blame snapshot has zero lines. A missing
or invalid snapshot fails the owner build and preserves cached owners.

Regenerate the blame snapshot locally from saved per-file blame evidence:

```sh
python3 scripts/blame_snapshot.py /path/to/ownership-raw-with-blame.json
python3 scripts/build_owners.py
```

The script aggregates human line counts using first-match path rules and the
owner builder's path/bot exclusions. Saved raw blame already excludes binary, generated,
minified and large fixture contents. The committed snapshot uses the 2026-10-21
capture at `30723c0be55edde1a012a4cd3d25039c43002e26` and keeps that capture's
`generated_at` and source commit. Owner output records both alongside its own
build time. The daily extractor does not collect blame. Review and commit
snapshot updates with map changes; add new areas to the snapshot before building.

Rate limits retry once after `Retry-After`, or 60 seconds if absent. Transient
failures retry once after 5 seconds. Persistent rate limits during apply return
a partial summary; the next run resumes. Other persistent errors fail the run.
