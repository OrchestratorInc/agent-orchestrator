# Deployments

## Staging

AO Cloud staging runs the control plane as a stateless ECS/Fargate service.
PostgreSQL is the durable source of truth, so any healthy task can serve a
request. The current release scripts and verifier use one configured desired
task for staging.

## Release flow

Run from a clean checkout of the release commit, inside `cloud/`:

```bash
AWS_PROFILE=ao-cloud ./scripts/deploy-staging.sh
```

The offering a deploy rolls out (served providers, the default provider, the
capability gate, default organization capabilities and billing, paywalled
features, and both idle-pause thresholds) is checked in at
`scripts/offering/staging.env` and `scripts/offering/production.env`. Staging
serves `coder,freestyle` with `coder` as the default; NodeOps is retired and no
longer a default. A variable set in the deploying shell overrides the file,
including an explicit empty value (for example
`AO_CLOUD_CAPABILITY_GATED_PROVIDERS=` turns the gate off). Each value is
validated by `scripts/validate-hosted-settings.py --offering` before anything is
built, and every one is written to the task explicitly.

The script takes an optional release tag or Git SHA as its positional argument
(default: the current commit). That ref must resolve to the clean checkout's
current commit. `AO_CLOUD_RELEASE` labels the running hosted service; it does
not select the input to this deployment script. Options:

- `--rolling-200-100` rolls the service with `maximumPercent: 200` and
  `minimumHealthyPercent: 100` instead of the default 100/0 (see
  [Rollout strategy](#rollout-strategy)).
- `SKIP_FREESTYLE_BAKE=1` skips the Freestyle snapshot bake and reuses the
  snapshot ids already in the Freestyle secret, so a Freestyle outage cannot
  block a Coder release. New Freestyle sessions then download the current
  worker from the control plane before starting.

The script:

1. Builds separate non-root, read-only `linux/amd64` control-plane and worker
   images.
2. Pushes immutable release tags to `ao-cloud-control-plane` and
   `ao-cloud-worker`.
3. Requires both ECR scans to finish with no critical or high findings.
4. Verifies that both images contain the same executable `ao-worker`, then
   registers a digest-pinned API task with `/ao-worker` as its packaged worker
   path and records the worker image digest on the task definition.
5. Runs `/ao-cloud-migrate` as a one-off Fargate task and stops on failure.
6. Rolls the API service with its configured one task only after migrations succeed.
7. Waits for ECS and the ALB target to become healthy.

The verifier requires exactly one desired/running task, no pending task, one task
definition revision, one target, healthy targets, and a deployment alarm that is
`OK`. Interrupted deployment scripts stop an unfinished migration task.

Re-running the same release reuses its immutable ECR image. A new code version
creates new task-definition revisions; it never mutates an older image tag.

## Runtime resources

- ECS cluster: `ao-cloud-staging`
- ECS service: `ao-cloud-staging-api`
- API task family: `ao-cloud-staging-api`
- migration task family: `ao-cloud-staging-migrate`
- PostgreSQL: `ao-cloud-staging-storage`
- public HTTPS ALB: `ao-cloud-staging-public`
- hostname: `staging-api.aoagents.dev`
- target group: `ao-cloud-staging-public-cp`
- CloudWatch log group: `/ao-cloud/staging/control-plane` (30-day retention)
- ECR repositories: `ao-cloud-control-plane` and `ao-cloud-worker` (immutable
  tags, 30-image retention)

The ALB probes `/readyz`, which checks both draining state and database
connectivity. Deployment scripts set one desired task and, by default,
`minimumHealthyPercent: 0`, so a rollout can briefly leave no healthy task. The
deployment circuit breaker rolls back failed starts, and the
`ao-cloud-staging-target-5xx` alarm rolls back a release with sustained target
5xx responses. `ao-cloud-staging-unhealthy-targets` tracks unhealthy replicas.

### Rollout strategy

Both deploy scripts default to `maximumPercent: 100` and
`minimumHealthyPercent: 0`: ECS stops the old task before the new one is
healthy. No second control plane ever runs, but the service has no healthy
target for the new task's start plus health checks (about six minutes in
production).

`--rolling-200-100` (on `deploy-staging.sh` and `promote-production.sh`) sets
`maximumPercent: 200` and `minimumHealthyPercent: 100` instead. The new task
must pass its ALB health checks before the old one is drained, so there is no
outage. Trade-offs:

- Two control-plane tasks run side by side for the overlap. Terminal relay
  affinity is process-local, so while both run a browser terminal and its
  worker can sit on different tasks and use the slower durable Postgres path,
  and connections held by the old task reconnect once it drains.
- The old target drains for the target group's deregistration delay: 300s on
  `ao-cloud-production-public-cp` (30s on `ao-cloud-staging-public-cp`). A
  production rollout therefore takes about five minutes longer, and the
  post-rollout verification ignores targets still in `draining` and waits up to
  six minutes. Lowering the production delay (for example to 60s with
  `aws elbv2 modify-target-group-attributes`) shortens this; the scripts do not
  change it.

ECS keeps whatever the last deploy set: a later deploy without the flag
returns the service to 100/0.

### Freestyle snapshots

When the deployment serves Freestyle, each release bakes new harness snapshots
from the exact control-plane digest (`scripts/publish-freestyle-snapshot.sh`),
records them in the Freestyle secret, and hands them to the new task. The bake
stages the worker binaries in the environment's artifacts bucket
(`ao-cloud-staging-artifacts`, `ao-cloud-production-artifacts`); a production
bake refuses a staging bucket.

Old harness snapshots are pruned only after the new task is verified healthy.
The prune keeps the two newest per harness and never deletes a snapshot named
by the Freestyle secret, the task that ran before the deploy, the task now
running, or any task definition the staging or production service is running
or rolling to. If any of those cannot be read it deletes nothing, and a failed
prune never fails the deploy.

## Monitoring

```bash
AWS_PROFILE=ao-cloud ./scripts/configure-monitoring.py --dry-run
AWS_PROFILE=ao-cloud ./scripts/configure-monitoring.py
```

This idempotently enforces 30-day staging and 90-day production log retention,
preserves the target-5xx deployment rollback alarms, and configures
unhealthy-target, latency, ECS CPU/memory, and RDS
CPU/free-storage/free-memory alarms. The `ao-cloud` dashboard shows ALB, ECS,
and RDS metrics for both environments. Set `AO_CLOUD_ALERT_TOPIC_ARN` to attach
SNS alarm and recovery notifications; otherwise the alarms have no human
notification action.

## Secrets

ECS reads these Secrets Manager entries through the dedicated staging execution
role:

- `ao-cloud/staging/workos`
- `ao-cloud/staging/database-url`
- `ao-cloud/staging/migration-database-url`
- `ao-cloud/staging/provider-secret-key`
- `ao-cloud/staging/coder` (required while staging serves Coder)
- `ao-cloud/staging/freestyle` (required while staging serves Freestyle)
- `ao-cloud/staging/nodeops` (only if NodeOps is explicitly listed; retired)
- `ao-cloud/staging/stripe` (optional; billing is off without it)
- `ao-cloud/staging/worker`
- `ao-cloud/repository-broker`

The API task gets only the runtime database credential. The elevated migration
credential is available only to the one-off migration task. Secrets are not
baked into the image or task-definition environment.

`nodeops` is a JSON secret with `base_url`, `api_key`, `default_shape`,
`default_rootfs`, `ingress`, `ssh_key_path`, `region`, and
`worker_token_ttl`. `worker` contains `signing_key`,
`max_active_sandboxes_per_org`, `sandbox_reconcile_interval`,
`sandbox_startup_timeout`, and `worker_heartbeat_timeout`. Deployment validates
every field before building or registering a task, and ECS injects each value
directly from its environment-scoped secret. Provider auto-pause is
intentionally absent from this deployment configuration.

`coder` is a JSON secret with `url`, `token`, `owner`, `template_id`,
`template_ids`, `agent_name`, `parameters_json`, `durable_root`, and
`worker_token_ttl`. `template_ids` is the comma-separated list of templates on
the shared Coder that sessions on AO's own Coder may pick (the default
`template_id` is always allowed); the template picker hides the rest and a
session on any other template is refused. Leave it empty to allow every
template. Bring-your-own Coder connections are not limited.
`durable_root` is the approved template's exact persistent-volume mount point,
not a deployment-wide assumption about the Coder user's home. It must be an
absolute normalized non-root path. `parameters_json` must be a JSON object whose
values are strings; use `{}` when the approved template has no parameters. The
deployment validates every provider in `AO_CLOUD_SANDBOX_PROVIDERS`. A
single-provider deployment removes the other provider's environment variables
and secret references. A multi-provider deployment, such as `nodeops,coder`,
retains both. The ECS execution role needs `secretsmanager:GetSecretValue` for
each configured provider's environment-scoped secret.

`freestyle` is a JSON secret with `api_key`, `default_snapshot`,
`worker_token_ttl`, and `snapshot_by_harness` (a JSON string mapping
`claude-code`, `codex`, and `cursor` to snapshot ids). The bake keeps
`snapshot_by_harness` and `default_snapshot` current.

`stripe` is optional. When it exists it must hold `secret_key`,
`webhook_secret`, and `price_ids` (a plan-to-`price_` map, as an object or a
JSON string), and may hold `portal_configuration` (`bpc_...`) and
`exempt_orgs_created_before` (RFC 3339: organizations created earlier are never
billed). Production refuses a test key and staging refuses a live key. Only a
`ResourceNotFoundException` turns billing off; any other lookup error (expired
credentials, a missing permission, throttling) fails the deploy, so billing is
never switched off by accident. When the secret is removed, the next deploy
strips every Stripe variable and secret reference from the task.

`ao-cloud/repository-broker` is shared only by the production control plane,
environment control planes, and the server-side web BFF. It is a JSON secret
with `auth_token` and `staging_control_token`, each containing at least 32
random characters. `auth_token` authenticates staging-to-production capability
validation and redemption. `staging_control_token` authenticates the web BFF
to the staging control plane. Neither value is exposed through a
`NEXT_PUBLIC_` variable.
Both ECS execution roles need `secretsmanager:GetSecretValue` for this one
secret in addition to their environment-scoped secrets.

The configured NodeOps `default_rootfs` must provide the unprivileged
`ao-worker` account plus `bash`, `git`, `claude`, `codex`, `cursor-agent`, and
`runuser`. Build the versioned AO rootfs from
`nodeops/Sandbox.Dockerfile` with `scripts/publish-nodeops-template.sh`, verify
it in staging, then set `default_rootfs` to that template name. The reconciler
copies the release's fenced `ao-worker` and AO hook helper binaries into the
rootfs, owns `/workspace` as `ao-worker`, and launches the worker under that
account. Claude auto-update is disabled for worker-launched processes so a
running VM cannot replace or remove its pinned executable. A missing harness
disables that agent terminal but does not stop workspace transport.
The separately scanned worker image is the canonical local/reference runtime,
but CreateOS does not consume that OCI image directly.

Database password rotation must update the corresponding URL secret before the
next rollout. For an immediate WorkOS or runtime database secret rotation,
force a new ECS deployment so every replica fetches the new value.

## Network boundary

The staging API is exposed only through HTTPS at
`https://staging-api.aoagents.dev`. Cloudflare serves DNS only; ACM terminates
TLS on the public ALB. The ALB security group admits port 443 and the task
security group admits port 8080 only from that ALB. Tasks still need to move to
private subnets with NAT or VPC endpoints instead of public task IPs.

## Rollback

ECS automatically returns to the last healthy task definition when startup,
health checks, or the deployment alarm fail. To select an older healthy release
manually:

```bash
AWS_PROFILE=ao-cloud \
  ./scripts/rollback-release.sh staging ao-cloud-staging-api:<revision>

AO_CLOUD_APPROVE_PRODUCTION_ROLLBACK=1 \
  AWS_PROFILE=ao-cloud \
  ./scripts/rollback-release.sh production ao-cloud-production-api:<revision>
```

The guarded script requires a digest-pinned task definition from the correct
environment, waits for ECS stability, and verifies tasks, targets, and alarms.
It does not reverse database migrations.

Schema migrations must remain backward-compatible with the previous API
revision so that rolling deploys and application rollback are safe.

## Production promotion

Production is deliberately a separate environment. It has its own PostgreSQL
instance, ECS cluster, service, ALB, IAM roles, logs, alarms, and Secrets Manager
entries. Staging and production share the immutable control-plane/worker image
digests and the narrowly scoped repository-broker authentication secret; their
databases, provider credentials, workers, and application state remain
separate.

Run this only after the release is healthy in staging:

```bash
AO_CLOUD_APPROVE_PRODUCTION=1 \
  AWS_PROFILE=ao-cloud \
  ./scripts/promote-production.sh [--rolling-200-100] [release]
```

Production declares its own offering in `scripts/offering/production.env`
(today: `coder` only). It no longer has to equal staging's: its providers must
be a subset of the providers the healthy staging task serves, so production
never runs a provider staging has not exercised. Only the
`ao-cloud/production/<provider>` secrets of the providers production serves are
required. Freestyle runs in production only when it is explicitly listed there.

The production script does not build images. It reads the release and both
digest-pinned artifacts from the healthy staging service, requires each ECR
scan to be complete with no high or critical findings, and refuses a requested
release that is not currently running in staging. It verifies both services
before changing production. New production task revisions are derived from the
existing production definitions, not staging, so production-only secrets survive
and staging variables, URLs, log groups, or secret ARNs cannot cross the
environment boundary. It then:

1. Validates the production offering against staging's providers, then the
   production-scoped secret documents of each provider production serves, the
   worker secret, and the optional `ao-cloud/production/stripe` secret.
2. Registers production API and migration task definitions using the exact
   control-plane digest and records the exact worker digest.
3. Runs `/ao-cloud-migrate` against production as a one-off task.
4. Refreshes the restricted runtime role's grants, including default privileges
   for objects created by future migrations.
5. Leaves the existing API service untouched if migration or grant application
   fails.
6. Rolls one production task, waits for ECS stability, and requires the ALB target
   to be healthy.
7. Verifies that production is running the same image digests and release as
   staging.

This promotes migration code, not staging data. Each environment retains its
own users, organizations, projects, sessions, events, and credentials.

### Production resources

- PostgreSQL: `ao-cloud-production-storage` (encrypted, single-AZ
  `db.t4g.small`, 7-day backups, deletion protection)
- ECS cluster: `ao-cloud-production`
- ECS service: `ao-cloud-production-api`
- API task family: `ao-cloud-production-api`
- migration task family: `ao-cloud-production-migrate`
- public HTTPS ALB: `ao-cloud-production-public`
- hostname: `api.aoagents.dev`
- target group: `ao-cloud-production-public-cp`
- CloudWatch log group: `/ao-cloud/production/control-plane` (90-day retention)
- control plane: one fixed replica while terminal relay affinity is process-local
- deployment alarms: `ao-cloud-production-target-5xx` and
  `ao-cloud-production-unhealthy-targets`

The service reads only these production-scoped secrets:

- `ao-cloud/production/workos-next` (WorkOS Production plus the `legacy_*` WorkOS
  Staging values; replaces `ao-cloud/production/workos`)
- `ao-cloud/production/database-url`
- `ao-cloud/production/migration-database-url`
- `ao-cloud/production/provider-secret-key`
- `ao-cloud/production/github`
- `ao-cloud/production/coder` (required while production serves Coder)
- `ao-cloud/production/freestyle` (required only once production lists
  Freestyle)
- `ao-cloud/production/stripe` (optional; billing is off without it)
- `ao-cloud/production/worker`
- `ao-cloud/repository-broker`

The production provider, `stripe`, and `worker` documents use the same schemas
as staging. Promotion reads and validates production values and never copies
secret values or ARNs from staging. A provider production stops serving (for
example NodeOps) has its variables and secret references removed from the task.

Production signs users in through the WorkOS Production environment, with
AuthKit on `auth.orchestrator.inc` and Google sign-in on
`login.orchestrator.inc`. Staging keeps the WorkOS Staging environment.
Production accounts created before the move keep their WorkOS Staging IDs:
`scripts/workos-copy-environment.py` copied each user and organization with its
Staging ID as `external_id`, which the control plane uses at sign-in. Re-run the
copy (it is idempotent) for users who sign up through WorkOS Staging while the
`legacy_*` keys in `ao-cloud/production/workos` keep that environment's tokens
accepted.

### Promotion and rollback limits

If a migration fails, promotion stops before the API changes. If new API tasks
fail startup or health checks, the ECS deployment circuit breaker restores the
last healthy task definition. The release script also checks the final task
definition so that an automatic rollback cannot be reported as a successful
promotion.

Application rollback does not reverse a completed database migration. Every
production migration must therefore use an expand-and-contract sequence and
remain compatible with the previous API release. Destructive cleanup belongs in
a later release after the old application version can no longer run.

Migration processes default to a 15-minute
`AO_CLOUD_MIGRATION_TIMEOUT`. Context cancellation bounds advisory-lock waits
and blocked DDL. If an ECS waiter or script is interrupted before migration
completion, the deployment trap stops that task instead of leaving it running
without an owner.

### Public ingress

Staging and production use internet-facing HTTPS ALBs:

- `ao-cloud-staging-public` for `staging-api.aoagents.dev`
- `ao-cloud-production-public` for `api.aoagents.dev`

ACM certificates in `eu-north-1` terminate TLS. Cloudflare keeps the API and ACM
validation CNAMEs in DNS-only mode. Each ECS service is attached only to its
public target group, and `/healthz` plus `/readyz` are verified through the
public hostnames. CloudWatch deployment alarms and the operations dashboard use
the public ALB and target-group dimensions.

The old internal ALBs and target groups have no ECS targets and can be deleted
after the public ingress observation period. Remaining network work is to move
tasks into private subnets with NAT or the required VPC endpoints.

Fargate tasks currently use the existing VPC's public subnets with public IPs,
while security groups admit application traffic only from an ALB and database
traffic only from the environment's task group.

### Authentication and callback readiness

Desktop WorkOS login uses PKCE and the custom `ao-app://callback` redirect. The
public API hostname is therefore not a WorkOS login callback: the desktop app
only needs its Cloud client base URL changed to the environment's HTTPS
hostname. Access-token verification and user synchronization are stateless
across API replicas, with durable identity and organization state in PostgreSQL.
No ALB stickiness is required.

GitHub App setup, OAuth verification, repository synchronization, disconnect,
and webhook handlers are implemented with PostgreSQL-backed state:

- setup URL: `https://api.aoagents.dev/api/cloud/v1/github/install/setup`
- OAuth callback:
  `https://api.aoagents.dev/api/cloud/v1/github/oauth/callback`
- webhook URL: `https://api.aoagents.dev/api/cloud/v1/github/webhooks`

The shared GitHub App routes these three global URLs to production. Staging must
not independently enable the same App credentials: its callback state would
live in a different database. Production creates scratch repositories only
after reserving an idempotency record, then issues an opaque capability bound
to the repository, installation, user, and target environment. The server-side
web BFF passes that capability directly to the authenticated environment
control-plane endpoint; browser responses never contain it. Environments
encrypt it with their provider credential key and redeem it server-to-server
for short-lived, repository-scoped installation tokens when workers check out.
Production reads the App ID, slug, client credentials, RSA
private key, webhook secret, and base64 32-byte state key from Secrets Manager
through the ECS task definition. `AO_CLOUD_PUBLIC_URL` is
`https://api.aoagents.dev`.
Configure the App with at least organization `Members: read`; AO uses that
permission to verify active organization-admin authority before binding an
installation. GitHub sends the `installation` and `installation_repositories`
events to every App automatically, so they do not appear in the selectable
event list.

`GET /github/healthz` signs an App JWT, calls GitHub's authenticated `/app`
endpoint, and verifies the configured App ID and slug. It returns `200` with
`status: ok` only when that authenticated round trip succeeds. It intentionally
does not enforce a permission policy. Staging returns
`503` with `status: disabled`; production returns `503` with
`status: unavailable` when credentials are present but GitHub cannot be
validated. The result is cached briefly to avoid turning the diagnostic into a
GitHub API amplification path. Do not use this route as the ALB health check:
GitHub outages must not restart otherwise healthy control-plane tasks.

No callback or webhook relies on replica memory. Callback routes, encrypted
PKCE verification state, installation routing, repository grants, and the
leased webhook inbox are durable in the production database, so any healthy
replica can receive a request and no ALB stickiness is required.

## Go-live checklist

The order matters: the capability gate turns on in the same promotion that
rolls the release, so organizations must be entitled first.

### 1. Production secrets

| Secret | When | Contents |
| --- | --- | --- |
| `ao-cloud/production/coder` | already exists | unchanged |
| `ao-cloud/production/freestyle` | before production lists Freestyle | `api_key`, `default_snapshot`, `worker_token_ttl`, `snapshot_by_harness` (the first bake overwrites the last two) |
| `ao-cloud/production/stripe` | before billing goes live | `secret_key` (live `sk_live_`/`rk_live_`), `webhook_secret`, `price_ids`, optional `portal_configuration` and `exempt_orgs_created_before` |
| `ao-cloud/production/support` | optional, any time | `token` (32+ chars): enables the operator support lookup (`docs/support-runbook.md`) |

Also before production lists Freestyle: create the
`ao-cloud-production-artifacts` S3 bucket (or set
`AO_CLOUD_PRODUCTION_ARTIFACTS_BUCKET`), and grant
`ao-cloud-production-execution-role` `secretsmanager:GetSecretValue` on each
new secret, including `support`: a task whose execution role cannot read a
referenced secret never starts, and the deploy rolls back. Register the production Stripe webhook endpoint against
`https://api.aoagents.dev` and use its signing secret as `webhook_secret`. Subscribe it to all events the control plane handles: `checkout.session.completed`, `checkout.session.expired`, `customer.subscription.created`, `customer.subscription.updated`, `customer.subscription.deleted`, `invoice.paid`, `invoice.payment_failed`, `product.updated` (staging was missing the last two until 2026-10-11).

### 2. WorkOS organization metadata

With `AO_CLOUD_CAPABILITY_GATED_PROVIDERS=coder,freestyle`, an organization
can use a gated provider only when its WorkOS metadata `capabilities` lists it;
organizations without the key get `AO_CLOUD_DEFAULT_ORG_CAPABILITIES`
(`freestyle`).

- B2B (contract) organizations: `capabilities=coder` and `billing=contract`.
  Set this on every production organization that uses Coder today **before**
  promoting the gated release, or it loses cloud sessions.
- The internal organization (dogfood): `capabilities=coder,freestyle` and
  `billing=contract`, in both WorkOS environments, so the team exercises both
  providers and is never billed.
- B2C self-serve organizations need nothing: they get the defaults and
  `self_serve` billing.

Staging and production share one WorkOS environment (the same API key), so
metadata set for a staging test is live for that organization in production too.
Production ignores it until it runs a gated release.

Personal organizations (no WorkOS organization) cannot carry metadata. Before
gating `coder` in production, list personal organizations with live Coder
sandboxes; their owners must move to a tagged WorkOS organization, or production
must leave `coder` ungated (`AO_CLOUD_CAPABILITY_GATED_PROVIDERS=freestyle`) until
they do.

The control plane caches organization metadata for a few minutes, so allow for
that before testing a change.

### 3. Rollout order

1. Staging: set the internal organization's metadata in WorkOS, then
   `deploy-staging.sh` (staging.env gates `coder,freestyle`). Verify a
   `capabilities=coder` organization gets Coder, an organization without
   metadata gets Freestyle only, and `/me` `offering` matches.
2. Billing in staging: with `ao-cloud/staging/stripe` present, verify Checkout,
   the webhook, and that a `billing=contract` organization is never charged.
3. Production metadata: tag every existing B2B organization and the internal
   organization in WorkOS (step 2).
4. Clear the worker image scan: the promotion refuses non-allowlisted
   CRITICAL/HIGH findings. Debian base packages (gnutls28, cyrus-sasl2 as of
   2026-10-11) can surface new ones with no fix yet; assess each before
   adding it to the shared allowlist, never with a one-off override.
5. Promote Coder only: `promote-production.sh --rolling-200-100` with
   production.env as checked in (`coder`, gate on, billing off unless the
   Stripe secret exists).
6. Freestyle in production: create the Freestyle secret and artifacts bucket,
   set `AO_CLOUD_SANDBOX_PROVIDERS=coder,freestyle` in
   `scripts/offering/production.env`, and promote again.
7. Billing in production: create `ao-cloud/production/stripe` and promote
   again. Organizations created before `exempt_orgs_created_before` stay
   exempt.

Rollback: set `AO_CLOUD_CAPABILITY_GATED_PROVIDERS=` (empty) in the promoting
shell and promote again to open every served provider to every organization,
or use `rollback-release.sh` for the previous task definition.
