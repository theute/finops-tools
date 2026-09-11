# finops-tools

FinOps command-line tools. The repository is a Go monorepo with a shared **core** library and a **finops** CLI built with [Cobra](https://github.com/spf13/cobra).

## Layout

| Path | Module | Role |
|------|--------|------|
| `core/` | `github.com/openshift-online/finops-tools/core` | Business logic (no CLI/HTTP dependencies) |
| `cli/` | `github.com/openshift-online/finops-tools/cli` | Cobra commands; calls into `core` |
| `backend/` | `github.com/openshift-online/finops-tools/backend` | HTTP server; calls into `core` |
| `go.work` | — | Ties modules together for local development |

The HTTP API is a separate module that imports **`core` only** — it must never depend on `cli` (enforced by `backend/boundary_test.go`).

### Package map (`cli/internal`)

| Package | Role |
|---------|------|
| `cmd/` | Cobra wiring only: `<noun>.go` + `<noun>_<verb>.go` (e.g. `report_create.go`) |
| `output/` | Human-readable tables and `--format` handlers |
| `format/` | Currency formatting for CLI output |
| `configstore/` | FinOps YAML config read/write |
| `snowflakeoauth/`, `snowflakecred/` | Red Hat SSO OAuth login and token storage |
| `account/` | Account login flows (`config account add` business logic; not the `cmd` noun files) |
| `aws/`, `awsauth/`, `awslogin/`, `awsrole/` | Credentials, auth orchestration, SAML, role ARNs |
| `report/` | HTML templates and charts (distinct from `core/report` data assembly) |
| `progress/` | Progress lines on stderr |

`core/` grows by domain noun (`core/cost`, `core/report`, `core/snowflake`), not by CLI verb. Shared target/credential orchestration stays in `cli/` until a third command needs it; see `.cursor/rules/cli-commands.mdc` for when to split `cmd/` into per-noun subpackages.

## CLI commands

Every command uses **singular root nouns** with **`finops <noun> <verb>`** for core ops, or **`finops config <sub-resource> <verb>`** for setup (e.g. `finops config account add`, `finops account get-cost`). See `.cursor/rules/cli-commands.mdc` for conventions when adding commands.

## Requirements

- Go 1.24+

## Development

From the repository root (uses `go.work`):

```bash
go work sync
make test
make lint
make install-hooks        # git push runs make lint (same golangci-lint as CI)
make build
make build-backend
./bin/finops --help
./bin/finops-backend      # starts HTTP server on :8080 (see HTTP API below)
```

`make lint` uses golangci-lint v2.12.2 and `.golangci.yml`, and reports only issues introduced since the merge-base with `origin/main` (local equivalent of GitHub Actions `only-new-issues` / `--new-from-patch`). Override the baseline with `make lint LINT_NEW_FROM=<sha>` to match a specific CI event. `make lint-all` scans the whole tree, including findings already on main. After `make install-hooks`, `git push` runs `make lint`. Bypass with `git push --no-verify` or `SKIP_LINT=1 git push`.

Or without Make:

```bash
go test ./core/... ./cli/... ./backend/...
go run ./cli/cmd/finops --help
go build -o bin/finops ./cli/cmd/finops
go run ./backend/cmd/finops-backend
go build -o bin/finops-backend ./backend/cmd/finops-backend
```

Edits under `core/` are picked up immediately by the CLI and HTTP server (workspace + `replace` in module `go.mod` files).

## HTTP API

The **`finops-backend`** HTTP server exposes a subset of FinOps capabilities for cluster deployment. It uses environment variables for Snowflake credentials (no local config files).

**Security note:** The MVP has **no HTTP authentication**. Restrict access at the network layer (cluster-internal Route, firewall rules) until auth and service-account Snowflake credentials are added.

OpenAPI 3.0 spec: embedded in the binary and served at `GET /openapi.yaml` (source: [`backend/internal/openapi/openapi.yaml`](backend/internal/openapi/openapi.yaml))

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/openapi.yaml` | OpenAPI 3.0 specification (YAML) |
| `GET` | `/livez` | Liveness probe; process is up (no external deps) |
| `GET` | `/readyz` | Readiness probe; Snowflake must be reachable when configured |
| `GET` | `/health` | Alias for `/livez` |
| `POST` | `/v1/snowflake/query` | Run read-only SQL against Snowflake (`SELECT`, `WITH`, `SHOW`, `DESCRIBE`, `DESC`, `EXPLAIN`, `LIST`) |
| `GET` | `/v1/aws/accounts/historical-count` | Daily AWS linked-account counts per payer (time series) |

**Query request:**

```json
{
  "sql": "SELECT CURRENT_USER(), CURRENT_ROLE()",
  "connection": "sandbox"
}
```

The optional `connection` field selects a named Snowflake environment (see below). When omitted, the default connection is used. Clients may also pass `X-FinOps-Snowflake-Connection` when the JSON field is empty.

**Query response:**

```json
{
  "columns": ["CURRENT_USER()", "CURRENT_ROLE()"],
  "rows": [["user@example.com", "MY_ROLE"]],
  "row_count": 1,
  "truncated": false
}
```

#### AWS accounts historical count

`GET /v1/aws/accounts/historical-count` returns daily snapshots of linked AWS account counts from `HCMFINOPSSOURCE_DB.MARTS.AWS_ACCOUNTS_HISTORICAL_COUNT`. For each payer and calendar day, the API keeps the latest snapshot that day (by `TIMESTAMP`, then `RUN_ID`).

| Query param | Default | Description |
|-------------|---------|-------------|
| `from` | none | Start date `YYYY-MM-DD` (inclusive) |
| `to` | none | End date `YYYY-MM-DD` (inclusive) |
| `payer_account_id` | none | 12-digit AWS payer account filter |
| `aggregate` | `payer` | `payer` = one series per payer per day; `sum` = totals across payers per day |
| `connection` | default Snowflake connection | Query param or `X-FinOps-Snowflake-Connection` header |

**Per-payer response** (`aggregate=payer`, default):

```json
{
  "aggregate": "payer",
  "from": "2026-01-01",
  "to": "2026-03-01",
  "data": [
    {
      "date": "2026-01-19",
      "payer_account_id": "123456789012",
      "nb_active_accounts": 100,
      "nb_closed_accounts": 1,
      "nb_deleted_accounts": 0
    }
  ],
  "row_count": 1,
  "truncated": false
}
```

**Summed across payers** (`aggregate=sum`):

```json
{
  "aggregate": "sum",
  "data": [
    {
      "date": "2026-01-19",
      "nb_active_accounts": 300,
      "nb_closed_accounts": 3,
      "nb_deleted_accounts": 0
    }
  ],
  "row_count": 1,
  "truncated": false
}
```

### Environment variables

#### Server

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `FINOPS_BACKEND_ADDR` | no | `:8080` | Listen address |
| `FINOPS_BACKEND_MAX_ROWS` | no | `1000` | Max rows returned per generic Snowflake query |
| `FINOPS_BACKEND_QUERY_TIMEOUT` | no | `60s` | Per-query timeout |
| `FINOPS_BACKEND_AWS_ACCOUNTS_HISTORICAL_TABLE` | no | `HCMFINOPSSOURCE_DB.MARTS.AWS_ACCOUNTS_HISTORICAL_COUNT` | Snowflake table for account-count history |
| `FINOPS_BACKEND_AWS_ACCOUNTS_HISTORICAL_MAX_ROWS` | no | `10000` | Max rows returned by `/v1/aws/accounts/historical-count` |

#### Snowflake

Set `SNOWFLAKE_CONNECTIONS` to a comma-separated list of lowercase names (e.g. `sandbox`, or `preprod,sandbox` when both are configured). Each connection uses prefixed env vars: `SNOWFLAKE_CONN_<NAME>_*` where `<NAME>` is uppercased in the key (`sandbox` → `SNOWFLAKE_CONN_SANDBOX_ACCOUNT`). Omit all Snowflake variables to start without Snowflake (`/v1/snowflake/query` returns 503).

| Variable | Required | Description |
|----------|----------|-------------|
| `SNOWFLAKE_CONNECTIONS` | yes | Comma-separated connection names |
| `SNOWFLAKE_DEFAULT_CONNECTION` | no | Default when the request omits `connection` (falls back to the first listed name) |
| `SNOWFLAKE_CONN_<NAME>_ACCOUNT` | yes | Account for connection `<NAME>` |
| `SNOWFLAKE_CONN_<NAME>_USER` | yes | User for connection `<NAME>` |
| `SNOWFLAKE_CONN_<NAME>_WAREHOUSE` | yes | Warehouse for connection `<NAME>` |
| `SNOWFLAKE_CONN_<NAME>_TOKEN` or `SNOWFLAKE_CONN_<NAME>_PRIVATE_KEY` | yes | Auth for connection `<NAME>` |
| `SNOWFLAKE_CONN_<NAME>_PRIVATE_KEY_FILE` | yes* | Read PEM from a mounted file instead of inline `PRIVATE_KEY` |
| `SNOWFLAKE_CONN_<NAME>_ROLE` / `_DATABASE` / `_SCHEMA` | no | Optional session defaults |

Private keys must be **unencrypted PEM** (PKCS#8 or PKCS#1). Decrypt encrypted keys externally before mounting or inlining them (for example `openssl pkcs8 -in encrypted.pem -out decrypted.pem`).

\*Provide one of token, inline private key, or private key file per connection.

Readiness (`/readyz`) checks only the **default** connection. Other connections connect lazily on first query.

#### Separate Secrets per environment

OpenShift uses split Secrets per connection (see [`deploy/openshift/deployment.yaml`](deploy/openshift/deployment.yaml)): a **config** Secret loaded via `envFrom` and a **key** Secret mounted as a file. This keeps private key material out of the process environment. `SNOWFLAKE_CONNECTIONS` and `SNOWFLAKE_DEFAULT_CONNECTION` are non-secret and are set as plain env vars in the Deployment.

| Source | Contents |
|--------|----------|
| Deployment `env` | `SNOWFLAKE_CONNECTIONS`, `SNOWFLAKE_DEFAULT_CONNECTION` |
| `finops-backend-snowflake-preprod-config` | `SNOWFLAKE_CONN_PREPROD_*` env keys (including `PRIVATE_KEY_FILE` path) |
| `finops-backend-snowflake-preprod-key` | `private_key` file mounted at `/etc/finops/snowflake/preprod/` |
| `finops-backend-snowflake-sandbox-config` | `SNOWFLAKE_CONN_SANDBOX_*` env keys (including `PRIVATE_KEY_FILE` path) |
| `finops-backend-snowflake-sandbox-key` | `private_key` file mounted at `/etc/finops/snowflake/sandbox/` |

Every name listed in `SNOWFLAKE_CONNECTIONS` must have matching config and key Secrets before the backend starts.

See [`deploy/openshift/secret.yaml.example`](deploy/openshift/secret.yaml.example) for a full multi-Secret example.

### Local run

```bash
export SNOWFLAKE_CONNECTIONS=sandbox
export SNOWFLAKE_DEFAULT_CONNECTION=sandbox
export SNOWFLAKE_CONN_SANDBOX_ACCOUNT=example-sandbox
export SNOWFLAKE_CONN_SANDBOX_USER=example-user
export SNOWFLAKE_CONN_SANDBOX_PRIVATE_KEY="$(cat ./sandbox_rsa_key.p8)"
export SNOWFLAKE_CONN_SANDBOX_WAREHOUSE=SANDBOX_WH

make build-backend
./bin/finops-backend
```

```bash
curl -s http://localhost:8080/livez
curl -s http://localhost:8080/openapi.yaml
curl -s -X POST http://localhost:8080/v1/snowflake/query \
  -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT CURRENT_USER(), CURRENT_ROLE()"}'
curl -s -X POST http://localhost:8080/v1/snowflake/query \
  -H 'Content-Type: application/json' \
  -d '{"connection":"sandbox","sql":"SELECT CURRENT_DATABASE()"}'
curl -s 'http://localhost:8080/v1/aws/accounts/historical-count?payer_account_id=123456789012&from=2026-01-01&to=2026-03-31'
curl -s 'http://localhost:8080/v1/aws/accounts/historical-count?aggregate=sum&from=2026-01-01'
```

### OpenShift deployment

Runtime namespace: **`finops-team--finops-tools-backend`** on cluster `prod-stable-spoke1-dc-rdu2`.

The TenantNamespace CR (`metadata.name: finops-tools-backend`) is applied in **`finops-team--config`**; the platform provisions **`finops-team--finops-tools-backend`**.

#### One-time namespace setup (platform / tenant admin)

Creating the namespace requires a **TenantNamespace** in `finops-team--config`. Most developers do not have permission for this API.

Ask a tenant admin to run once:

```bash
oc apply -f deploy/openshift/tenantnamespace.yaml
```

If you see `Forbidden` on `tenantnamespaces.tenant.paas.redhat.com`, you need that admin step — you cannot self-provision the namespace.

Confirm the namespace exists before deploying:

```bash
oc get namespace finops-team--finops-tools-backend
oc auth can-i create deployment -n finops-team--finops-tools-backend
```

#### Deploy the API (developers)

Full rebuild, push, and roll out:

```bash
make openshift-refresh
```

Or step by step:

1. Build and push the image:

```bash
make podman-build
make podman-push
```

2. Deploy workloads (do **not** apply `secret.yaml.example` with dummy values):

```bash
make openshift-apply
```

3. Create Snowflake Secrets when ready (config + key per connection listed in `SNOWFLAKE_CONNECTIONS`):

```bash
# Preprod (default connection)
oc create secret generic finops-backend-snowflake-preprod-config \
  --from-literal=SNOWFLAKE_CONN_PREPROD_ACCOUNT=example-preprod \
  --from-literal=SNOWFLAKE_CONN_PREPROD_USER=example-user \
  --from-literal=SNOWFLAKE_CONN_PREPROD_WAREHOUSE=PREPROD_WH \
  --from-literal=SNOWFLAKE_CONN_PREPROD_PRIVATE_KEY_FILE=/etc/finops/snowflake/preprod/private_key \
  -n finops-team--finops-tools-backend

oc create secret generic finops-backend-snowflake-preprod-key \
  --from-file=private_key=./preprod_rsa_key.p8 \
  -n finops-team--finops-tools-backend

# Sandbox
oc create secret generic finops-backend-snowflake-sandbox-config \
  --from-literal=SNOWFLAKE_CONN_SANDBOX_ACCOUNT=example-sandbox \
  --from-literal=SNOWFLAKE_CONN_SANDBOX_USER=example-user \
  --from-literal=SNOWFLAKE_CONN_SANDBOX_WAREHOUSE=SANDBOX_WH \
  --from-literal=SNOWFLAKE_CONN_SANDBOX_PRIVATE_KEY_FILE=/etc/finops/snowflake/sandbox/private_key \
  -n finops-team--finops-tools-backend

oc create secret generic finops-backend-snowflake-sandbox-key \
  --from-file=private_key=./sandbox_rsa_key.p8 \
  -n finops-team--finops-tools-backend

oc rollout restart deployment/finops-backend -n finops-team--finops-tools-backend
```

Or `make openshift-restart` after `make openshift-apply`.

4. Verify:

```bash
oc get pods,svc,endpoints,route -n finops-team--finops-tools-backend -l app=finops-backend
curl -s "https://$(oc get route finops-backend -n finops-team--finops-tools-backend -o jsonpath='{.spec.host}')/livez"
curl -s "https://$(oc get route finops-backend -n finops-team--finops-tools-backend -o jsonpath='{.spec.host}')/openapi.yaml"
```

Wire `serviceAccountName` in `deployment.yaml` when Red Hat provides the service account.

## Configuration

FinOps stores local settings in a YAML config file:

| OS | Default path |
|----|----------------|
| Linux / macOS | `$XDG_CONFIG_HOME/finops/config.yaml` or `~/.config/finops/config.yaml` |
| Windows | `%AppData%/finops/config.yaml` |

The file is created automatically on first `finops config account add`. Example:

```yaml
defaults:
  aws.auth_method: profile
  aws.linked_role: OrganizationAccountAccessRole
  cost.days: "30"
  cost.exclude_recent_days: "2"
aws:
  account_aliases:
    rh-control: "123456789012"
    osd-staging-1: "987654321098"
    osd-tenant-1:
      account_id: "111111111111"
      payer_alias: rh-control
      role: OrganizationAccountAccessRole
gcp:
  account_aliases: {}
snowflake:
  account_aliases:
    rhprod:
      account: ORG-ACCOUNT
      role: PUBLIC
      sso: prod
```

OAuth client ID and secret are **not** stored in `config.yaml`. Use a separate secrets file (default `~/.config/finops/snowflake-oauth.yaml`, mode `0600`) or environment variables.

Set defaults using fully qualified names (used when `--auth-method` is omitted on `config account add`):

```bash
finops config default set --name aws.auth-method --value profile
finops config default get --name aws.auth-method
finops config default set --name aws.linked_role --value OrganizationAccountAccessRole
finops config default set --name cost.exclude_recent_days --value 2
```

Cost query period defaults (`cost.days`, `cost.months`, `cost.from`, `cost.to`, `cost.exclude_recent_days`) apply to `finops account get-cost` and `finops report create` when the matching CLI flag is omitted. Set only one of `cost.days`, `cost.months`, or `cost.from` (optional `cost.to` with `cost.from`).

Register a **payer** account by **12-digit account ID** (login + save in config):

```bash
finops config account add aws 123456789012 --alias rh-control       # auth-method: flag, or config default, else saml
finops config account add aws 123456789012 --auth-method profile  # overrides config default
finops config account add aws 123456789012 --force
```

Register a **linked** account (authenticate to the payer first, then assume a role in the member account):

```bash
finops config account add aws 111111111111 --alias osd-tenant-1 --payer rh-control
finops config account add aws 111111111111 --payer rh-control --role CustomRole
```

The IAM role name defaults to `OrganizationAccountAccessRole`, or `defaults.aws.linked_role` in the finops config. The CLI builds `arn:aws:iam::<account-id>:role/<role-name>` automatically.

Without `--alias`, the account ID is used as the config key. Aliases are CLI-only; cost and AWS credential logic use 12-digit account IDs.

List registered accounts (payer vs linked):

```bash
finops config account list
finops config account list aws
finops config account list snowflake
```

### Snowflake (Red Hat SSO OAuth)

Query Snowflake using OAuth tokens from [Red Hat SSO](https://dataverse.pages.redhat.com/platform/snowflake/red-hat-sso-access/). The access token must include audience `dataverse-snowflake` and scope `session:role-any` (usually via IAM default client scopes / mappers, not by requesting scopes in the authorize URL). The CLI OAuth redirect URI is fixed at `http://127.0.0.1:8765/oauth/callback` (must be registered on the SSO client).

Session settings (account, role, warehouse, database, schema) are stored only in the finops config file (`~/.config/finops/config.yaml`). The CLI does **not** read `~/.snowflake/connections.toml` or Snowflake CLI connection profiles. Configure each alias with `finops config account add snowflake` flags and/or `snowflake.*` defaults below.

Store OAuth client credentials (never commit these):

```bash
finops config oauth-client set --client-id finops-tools-dataverse --client-secret "$SECRET"
# or: export FINOPS_SNOWFLAKE_OAUTH_CLIENT_ID=... FINOPS_SNOWFLAKE_OAUTH_CLIENT_SECRET=...
```

Optional defaults:

```bash
finops config default set --name snowflake.sso_issuer --value prod   # or stage (pre-prod Snowflake only)
finops config default set --name snowflake.oauth_audience --value dataverse-snowflake
# Override which registered alias finops snowflake uses (first account add sets this automatically):
# finops config default set --name snowflake.account_alias --value rhprod
# Shared session defaults when an alias omits role/warehouse/database/schema:
# finops config default set --name snowflake.warehouse --value MY_WH
# finops config default set --name snowflake.role --value MY_ROLE
```

Register a Snowflake account (opens browser for Red Hat SSO, stores refresh token in `~/.config/finops/snowflake-tokens.yaml`). A warehouse is required (per alias or via `snowflake.warehouse` default):

```bash
finops config account add snowflake myorg-sandbox --alias sandbox \
  --snowflake-role MY_ROLE \
  --warehouse MY_WH \
  --database MY_DB --schema MY_SCHEMA
finops config account add snowflake myorg-prod --alias prod --force   # re-login
```

Run SQL:

```bash
finops snowflake query --sql "SELECT CURRENT_USER(), CURRENT_ROLE()"
finops snowflake query --account-alias sandbox --sql "SELECT 1"
finops snowflake query --sql "SELECT 1" --format json
```

Manage AWS Organizations tags on an account (registered alias or 12-digit account ID):

```bash
finops tag list --account-alias rh-control
finops tag list --account-id 111111111111 --payer rh-control
finops tag add --account-alias rh-control --tag-key owner --tag-value team-a
finops tag add --account-id 111111111111 --tag-key owner --tag-value team-b --force --payer rh-control
finops tag update --account-alias rh-control --tag-key owner --tag-value team-c
finops tag update --account-id 111111111111 --tag-key env --tag-value prod --force --payer rh-control
```

List AWS Organizational Units (for discovering OU IDs to use with `--ou` on cost/report commands):

```bash
finops aws list-ous --payer rh-control
finops aws list-ous --payer rh-control --parent ou-abcd-12345678
finops aws list-ous --payer rh-control --format json
```

Move a linked account between OUs (or root) **within the same payer** (Organizations `MoveAccount`):

```bash
# Plan only
finops aws move-to-ou --payer rh-control --account-id 111111111111 --destination-ou ou-abcd-12345678 --dry-run

# Execute (requires --yes); --account-id accepts comma-separated IDs
finops aws move-to-ou --payer rh-control --account-id 111111111111 --destination-ou ou-abcd-12345678 --yes
finops aws move-to-ou --payer rh-control --account-id 111111111111,222222222222 --destination-ou ou-abcd-12345678 --yes
```

Same-payer only: billing stays with the payer; inherited SCPs / Control Tower / StackSets may change. For moving an account to a **different** payer organization, use `migrate-account` below.

Migrate a linked (member) account from one payer organization to another (AWS Organizations invite + accept):

```bash
# Plan only
finops aws migrate-account --account-id 111111111111 --from-payer rh-control --to-payer osd-staging-1 --dry-run

# Execute (requires --yes); --account-id accepts comma-separated IDs
finops aws migrate-account --account-id 111111111111 --from-payer rh-control --to-payer osd-staging-1 --yes
finops aws migrate-account --account-id 111111111111,222222222222 --from-payer rh-control --to-payer osd-staging-1 --yes
finops aws migrate-account --account-id 111111111111 --from-payer rh-control --to-payer osd-staging-1 \
  --destination-ou ou-abcd-12345678 --yes
```

`--account-id` accepts one or more comma-separated 12-digit AWS account IDs. The command invites from the destination payer, assumes into the member via the source payer (`OrganizationAccountAccessRole` or `--role`), accepts the handshake, rewrites that role's trust policy to the destination management account, optionally moves the account into a destination OU, then updates local `payer_alias`. Requires Organizations admin on both payers; SCPs/Control Tower may still block leave/accept. If accept succeeds but trust update fails, the account is already in the destination org while the role still trusts the source payer — assume from `--from-payer` and retry the trust update (not a rollback).

**Cost Explorer (`finops account get-cost`) requires payer accounts only.** Linked-account credentials are for member-account APIs, not payer-level billing queries.

Static secrets (API keys, etc.) for other tools live in `~/.config/finops/.env`; AWS sessions use `~/.aws/credentials` profiles.

### AWS global flags

These persistent flags are available on AWS-backed commands (`account`, `snapshot`, `report`, `tag`, `aws`, `config account`):

| Flag | Description |
|------|-------------|
| `--auth-method` | `saml` (default) or `profile`; when omitted, uses `defaults.aws.auth_method` from config |
| `--config` | Path to finops config file (default: OS-specific config dir) |
| `--credentials-file` | Path to AWS credentials file (default: `~/.aws/credentials`) |
| `--verbose` / `-v` | Log external commands (`klist`, `curl`, …) and selected AWS API calls (STS, Organizations, EC2, RDS, Cost Explorer) to stderr |

Verbose output uses two line prefixes: `+ "command" "args"` for external binaries and `+ AWS Service.Operation …` for cloud API calls. Organizations tag reads log `ListTagsForResource`; tag writes log `TagResource` (matching the AWS API operation names).

### AWS payer credentials

Store and verify temporary AWS credentials for a payer account (same profile layout as finops-mcp-aws):

```bash
finops config account add aws 123456789012
```

**Behavior:**

1. Looks up a profile derived from the account ID in `~/.aws/credentials`, then in `~/.aws/config` (shared config / SSO).
2. If the profile exists and STS validation succeeds, reports success without logging in again.
3. With `--auth-method saml` (default), if credentials are missing or invalid, runs a native Red Hat Kerberos + SAML login flow and merges temporary credentials into `~/.aws/credentials` (other profiles are preserved).
4. With `--auth-method profile`, SAML login is skipped. `finops config account add` uses an existing profile when valid (including a `~/.aws` profile named like `--alias`, e.g. `rh-control`); in an interactive terminal it prompts for access keys only when no matching profile exists. You can also configure the profile yourself (`aws configure`, `aws sso login`, etc.) and run `config account add` again to confirm it works.

**SAML prerequisites** (default login, and `--force`):

- Red Hat VPN connected
- Valid Kerberos ticket (`kinit`)
- Kerberos tools available locally (`klist`, usually present on managed RH laptops)

SAML account matching accepts:

- 12-digit AWS account ID (recommended for `finops config account add aws <id>`)
- SAML account display name (for example `rh-control`)
- `account/role` when a specific role name is required

#### Linked accounts (profile chaining without finops flags)

You can also configure role assumption in `~/.aws/config` and use `--auth-method profile`:

```ini
[profile rh-control]
# payer credentials (SAML output, SSO, or keys)

[profile osd-tenant-1]
role_arn = arn:aws:iam::111111111111:role/OrganizationAccountAccessRole
source_profile = rh-control
```

```bash
finops config account add aws 111111111111 --alias osd-tenant-1 --auth-method profile
```

STS validation must report the **linked** account ID. This registers credentials only; for finops metadata (`payer_alias`, `role`) use `--payer` (and optional `--role`) as shown above.

### Cost (AWS)

Fetch **Net Amortized Cost** from AWS Cost Explorer for a configurable date range (default: last 30 calendar days, or `defaults.cost.*` in config). Payer and linked account aliases are supported; linked accounts query Cost Explorer through the registered payer.

**Account selection uses exactly one mode** (combining modes is an error):

| Mode | Flags |
|------|-------|
| Explicit accounts | `--account-id` and/or `--account-alias` (optional `--payer` for unregistered member IDs) |
| Entire org | `--payer` alone |
| OU / org-root members | `--payer` + `--ou` (optional scope suffix per ID: `/`, `/*`, `/**`) |
| Tag match | `--payer` + `--tag KEY[=VALUE]` |

```bash
finops config account add aws 123456789012 --alias rh-control
finops account get-cost --account-alias rh-control
finops account get-cost --account-alias rh-control --days 7
finops account get-cost --account-alias rh-control --months 3
finops account get-cost --account-alias rh-control --from 2026-01-01 --to 2026-03-31
finops account get-cost --account-alias rh-control --exclude-recent-days 2   # omit last 2 days (AWS CE lag)
finops account get-cost --account-alias quay              # linked account (uses payer credentials)
finops account get-cost --account-id 123456789012
finops account get-cost --account-alias rh-control,osd-staging-1
finops account get-cost --account-id 123456789012 --format json
finops account get-cost --account-id 123456789012 --format csv
finops account get-cost --account-id 123456789012 --group-by service
finops account get-cost --account-id 123456789012 --group-by account
finops account get-cost --payer rh-control --account-id 333333333333   # member account via payer
finops aws list-ous --payer rh-control           # discover OU IDs
finops account get-cost --ou ou-abcd-12345678 --payer rh-control
finops account get-cost --ou ou-abcd-12345678/ --payer rh-control --days 7
finops account get-cost --ou 'ou-abcd-12345678/*' --payer rh-control
finops account get-cost --ou r-xxxx/ --payer rh-control            # accounts directly under org root
finops account get-cost --payer rh-control
finops account get-cost --payer rh-control --group-by ou
finops account get-cost --payer rh-control --tag organization
finops account get-cost --payer rh-control --tag organization="Hybrid Platform" --group-by service
finops report create costs --payer rh-control --tag env=prod -o prod.html
```

| Flag | Description |
|------|-------------|
| `--account-id` | One or more comma-separated **12-digit AWS account IDs** (provider-native IDs; unregistered members require `--payer`) |
| `--account-alias` | One or more comma-separated configured aliases (e.g. `rh-control`, or a linked alias such as `quay`) |
| `--ou` | One or more comma-separated AWS OU (`ou-xxxx-yyyyy`) or org-root (`r-xxxx`) IDs; requires `--payer`. Optional scope suffix per ID: bare or `/**` = full subtree (default), `/` = accounts directly in that parent only, `/*` = parent + immediate child OUs only |
| `--payer` | Registered payer alias. Alone selects all active org members; also required with `--ou` / `--tag`, or with unregistered `--account-id` IDs |
| `--tag` | Select org accounts by Organizations tag: `KEY` or `KEY=VALUE` (requires `--payer`) |
| `--skip-org-cache` | Bypass cached organization account/tag data (always fetch live from AWS) |
| `--refresh-org-cache` | Ignore cached organization data and refresh the cache from AWS (mutually exclusive with `--skip-org-cache`) |
| `--days` | Last N calendar days (mutually exclusive with `--months` and `--from`/`--to`) |
| `--months` | Last N calendar months from the 1st of the month (mutually exclusive with `--days` and `--from`/`--to`) |
| `--from` | Start date `YYYY-MM-DD` inclusive (optional `--to`; otherwise through the latest stable day) |
| `--to` | End date `YYYY-MM-DD` inclusive (requires `--from`; historical only — future dates are rejected) |
| `--exclude-recent-days` | Omit the last N UTC days from the end anchor (incomplete AWS CE data); default from `defaults.cost.exclude_recent_days` or `0` |
| `--verbose` / `-v` | Log external commands and selected AWS API calls to stderr (see [AWS global flags](#aws-global-flags)) |
| `--format` | `pretty-print` (default), `json`, or `csv` |
| `--quiet` | Suppress progress messages on stderr (cost/CSV/JSON still go to stdout) |
| `--workers` | Maximum concurrent workers for multi-account AWS queries (default: `25`, max: `1000`; use `1` for sequential) |
| `--group-by` | Group costs by dimension: `service`, `account` (linked AWS account ID), or `ou` (OU tree under the selection root; each row is subtree total, indented in pretty-print); includes share % and relative cost bars in `pretty-print` |
| `--provider` | `aws` (default). `gcp` is reserved for a future release |

`pretty-print` uses colors and Unicode bars when stdout is a TTY. Set `NO_COLOR=1` to disable; `FORCE_COLOR=1` forces colors when piping to a capable viewer.

### Account owner notification (AWS)

Gather **monthly cost trends** and **resource inventory** (EC2, RDS, Route53, S3, Lambda, load balancers, and more) for delete/keep decisions, then notify the account owner. The owner email is derived from the Organizations `owner` tag, appending `@redhat.com` when the tag value has no `@` sign.

Email is sent through **Gmail** using gcloud Application Default Credentials. By default the command only builds reports and prints a delivery plan. To send mail, pass `--send` with either `--yes` (owner addresses) or `--redirect-prefix` (test delivery to `PREFIX+<owner>@redhat.com`).

A Cost Explorer, inventory, or owner-tag failure on one account is recorded for that account and does not stop the rest of the run.

Gmail uses existing gcloud Application Default Credentials. finops does **not** modify `~/.config/gcloud/application_default_credentials.json`. API quota is billed to `hcmfinops` via client options.

```bash
# Sign in with gcloud (only if needed; run this yourself)
# --disable-quota-project: do not write your gcloud config project into ADC
gcloud auth application-default login \
  --disable-quota-project \
  --scopes=https://www.googleapis.com/auth/cloud-platform,https://www.googleapis.com/auth/gmail.send,https://www.googleapis.com/auth/gmail.metadata

# Verify finops can send Gmail with your existing ADC
finops config gmail login

# Plan only (no email sent)
finops account notify-owner --account-alias my-linked

# Test send to your inbox via plus-addressing
finops account notify-owner --payer rh-control --account-id 111111111111 --send --redirect-prefix finops

# Production send to owner addresses
finops account notify-owner --payer rh-control --ou ou-abcd-12345678 --group-by owner --send --yes
```

| Flag | Description |
|------|-------------|
| `--send` | Send emails via Gmail (default: plan only, no send) |
| `--yes` | With `--send`, deliver to resolved owner emails |
| `--redirect-prefix` | With `--send`, deliver to `PREFIX+<owner>@redhat.com` (test mode) |
| `--group-by` | `account` (default) or `owner` — one email per account or per owner |
| `--months` | Calendar months of cost history to include (default: `6`) |
| `--exclude-recent-days` | Omit the last N UTC days from the cost end anchor (incomplete AWS CE data); default from `defaults.cost.exclude_recent_days` or `0` |
| `--format` | Delivery summary format: `pretty-print` (default) or `json` |

Gmail API quota/billing defaults to the `hcmfinops` GCP project (via API client options, not by changing ADC). Override with `FINOPS_GMAIL_QUOTA_PROJECT`.

### Snapshot (AWS)

Find **EBS and RDS snapshots** older than a cutoff and estimate monthly storage cost. Account selection matches `finops account get-cost` (exactly one of `--account-id`/`--account-alias`, `--ou`, `--tag`, or `--payer` alone). Linked member accounts are scanned via role assumption from the payer.

```bash
finops snapshot list --account-alias rh-control
finops snapshot list --account-alias rh-control --older-than-days 365 --format json
finops snapshot list --payer rh-control
finops snapshot list --payer rh-control --tag organization
finops snapshot list --ou ou-abcd-12345678 --payer rh-control --types ebs
finops snapshot list --account-id 333333333333 --payer rhc --older-than-days 90 --format csv
```

| Flag | Description |
|------|-------------|
| `--older-than-days` | List snapshots older than this many days (default: `365`) |
| `--types` | Snapshot types to scan: `ebs`, `rds`, or comma-separated (default: `ebs,rds`) |
| `--regions` | Limit scan to comma-separated AWS regions (default: all enabled regions) |
| `--min-size-gib` | Skip snapshots smaller than this size in GiB (default: `0`) |
| `--account-id` / `--account-alias` / `--ou` / `--tag` / `--payer` | Same account selection as `finops account get-cost` |
| `--role` | Linked-account IAM role name (default: `defaults.aws.linked_role` in config) |
| `--format` | `pretty-print` (default), `json`, or `csv` |
| `--quiet` | Suppress progress messages on stderr |
| `--workers` | Maximum concurrent workers for multi-account AWS queries (default: `25`, max: `1000`; use `1` for sequential) |
| `--verbose` / `-v` | Log external commands and selected AWS API calls to stderr (see [AWS global flags](#aws-global-flags)) |

When Cost Explorer data is available, the summary shows **attributed** storage cost for listed snapshots (scaled to billed `EBS:SnapshotUsage` and `RDS:ChargedBackupUsage` for the last complete calendar month) and **account-wide** billed amounts (also in JSON as `summary.billed_costs`). Per-snapshot **$/MO** is each snapshot's share of that attributed cost; **—** on EBS means no incremental blocks. Without CE data, summary falls back to API estimates. Payer credentials need `ce:GetCostAndUsage` with `LINKED_ACCOUNT` scope.

### Reports

Generate HTML reports from configured accounts. Templates use Go's **`html/template`**, embedded in the CLI binary under `cli/internal/report/templates/`.

```bash
finops report list
finops report create costs --account-alias rh-control
finops report create costs --account-alias rh-control -o costs.html
finops report create costs --account-id 333333333333 --payer rhc -o member.html
finops report create costs --ou ou-abcd-12345678 --payer rh-control -o ou-costs.html
finops report create costs --payer rh-control -o all-members.html
finops report create costs --payer rh-control --tag env=prod -o prod.html
```

The **costs** template includes:

- Total net amortized cost for the selected period (same flags and config defaults as `account get-cost`)
- Breakdown by linked AWS account
- Breakdown by organizational unit (when org structure can be resolved)
- Breakdown by AWS service
- Daily cost trend chart (embedded SVG; works when opening the HTML file locally)

| Flag | Description |
|------|-------------|
| `template` | Positional argument: report template name (run `finops report list` for options) |
| `--format` | Output format (default: `html`) |
| `--account-id` / `--account-alias` / `--ou` / `--tag` / `--payer` | Same account selection as `finops account get-cost` (exactly one mode) |
| `--skip-org-cache` | Bypass cached organization account/tag data |
| `--refresh-org-cache` | Refresh organization cache from AWS |
| `--verbose` / `-v` | Log external commands and selected AWS API calls to stderr (see [AWS global flags](#aws-global-flags)) |
| `--output` / `-o` | Write HTML to a file instead of stdout |
| `--quiet` | Suppress progress messages on stderr (HTML still goes to stdout or `--output`) |
| `--workers` | Maximum concurrent workers for multi-account AWS queries (default: `25`, max: `1000`; use `1` for sequential; **costs** template only) |
| `--days`, `--months`, `--from`, `--to`, `--exclude-recent-days` | Same period options as `finops account get-cost` |

Progress lines (tag resolution, credential checks, Cost Explorer queries) are printed to **stderr** so you can redirect output safely, e.g. `finops account get-cost ... --format json > costs.json`.

Use `--quiet` to suppress progress messages.

Tag-based account selection caches organization account and tag listings via the shared finops cache service (`cli/internal/cache`) under `cache/org/<payer-account-id>.json` next to your config (default TTL: 1 hour). Use `--refresh-org-cache` to force a refresh or `--skip-org-cache` to always query AWS live.

When many linked accounts under the same payer are queried together (typical for `--payer` alone, `--tag`, or `--ou`), `finops account get-cost` uses **bulk Cost Explorer queries** grouped by linked account instead of one API call per account (usually a single CE call for up to 100 linked accounts; use `--workers` to parallelize batched CE calls when there are more than 100 accounts). `finops snapshot list` and `finops report create costs` use `--workers` (default `25`, max `1000`) to parallelize per-account scans, assume-role setup, and batched CE queries when selections are large.

## Cross-compile (local)

```bash
GOOS=linux GOARCH=amd64 go build -o bin/finops-linux-amd64 ./cli/cmd/finops
GOOS=windows GOARCH=amd64 go build -o bin/finops.exe ./cli/cmd/finops
GOOS=darwin GOARCH=arm64 go build -o bin/finops-darwin-arm64 ./cli/cmd/finops
```

## Releases

Releases are built with [GoReleaser](https://goreleaser.com/) on tag push (`v*`). Artifacts include **linux**, **darwin**, and **windows** (amd64 and arm64 where applicable).

1. Merge changes to the default branch.
2. Create and push a tag, e.g. `git tag v0.1.0 && git push origin v0.1.0`.
3. The [Release workflow](.github/workflows/release.yml) publishes binaries to GitHub Releases.

Download a release asset, extract it, and run:

```bash
finops --help
```

## CI

- **Test** (`.github/workflows/test.yml`): runs on pull requests and pushes to `main`/`master` (tests and builds CLI + API).
- **Release** (`.github/workflows/release.yml`): runs on version tags.
