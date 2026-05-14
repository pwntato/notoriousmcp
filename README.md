# NotoriousMCP

A self-hosted MCP (Model Context Protocol) server for notes, todo lists, files, and LLM memory — backed by AWS Lambda + DynamoDB + S3. Named after Notorious B.I.G.

**Tools available:** notes, todo lists, todos, files, user admin, status check  
**Auth:** Google OAuth 2.0 → opaque Bearer tokens (1h TTL, auto-refreshed)  
**IaC:** Terraform with GitHub Actions OIDC CI/CD  
**License:** MIT

---

## Table of Contents

- [Architecture](#architecture)
- [Cost Estimate](#cost-estimate)
- [Prerequisites](#prerequisites)
- [Step 1 — Google OAuth Credentials](#step-1--google-oauth-credentials)
- [Step 2 — Bootstrap Terraform State](#step-2--bootstrap-terraform-state)
- [Step 3 — Find Your Google Subject ID](#step-3--find-your-google-subject-id)
- [Step 4 — Set GitHub Secrets](#step-4--set-github-secrets)
- [Step 5 — Deploy](#step-5--deploy)
- [Step 6 — Approve Your Account](#step-6--approve-your-account)
- [Step 7 — Connect an MCP Client](#step-7--connect-an-mcp-client)
- [Local Development](#local-development)
- [User Management](#user-management)
- [Skill File](#skill-file)

---

## Architecture

```
MCP Client
    │  Authorization: Bearer <token>
    ▼
CloudFront  (HTTPS termination; no OAC — app handles its own auth)
    │
    ▼
Lambda Function URL  (AuthType: NONE, invoke mode: BUFFERED)
    │
    ├── DynamoDB  (single table — users, notes, todos, files, auth codes)
    ├── S3        (note and file content; metadata lives in DynamoDB)
    └── SSM       (secrets fetched at cold start — never in env vars)
```

**Auth flow (RFC 7591 + RFC 9728 — used by Claude Code and other MCP clients):**
1. Client discovers auth server via `GET /.well-known/oauth-authorization-server`
2. Client registers itself via `POST /register` (RFC 7591 dynamic client registration)
3. Client opens a browser to `/auth/login` → redirected to Google
4. Google redirects to `/auth/callback`; server exchanges the code for Google tokens, creates/updates user in DynamoDB, then redirects client to its local callback with an exchange code
5. Client POSTs the exchange code to `/auth/token` → receives an opaque Bearer token (1h TTL)
6. On expiry, server issues a new token via `X-New-Token` response header — clients auto-refresh

**Manual token flow** (for scripting/testing): visit `/auth/login` in a browser, complete the Google flow, copy the `?code=` from the redirect URL, then POST it to `/auth/token`.

New accounts start as **pending** and can only call `check_status`. An admin must promote them to **user** or **admin**.

**Admin bootstrap:** The `ADMIN_GOOGLE_IDS` SSM parameter holds a comma-separated list of Google subject IDs that are forcibly set to `admin` status on every login. This self-heals if admin status is accidentally removed.

---

## Cost Estimate

For a personal or small-team deployment with light usage (a few hundred MCP calls/day), AWS costs are roughly **$1–2/month**:

| Service | What you're paying for | Est. monthly cost |
|---------|------------------------|-------------------|
| Lambda | Invocations + duration (256MB arm64, ~100ms/call) | < $0.01 |
| DynamoDB | On-demand reads/writes at low volume | < $0.01 |
| S3 | Storage + requests for note/file content | < $0.10 |
| CloudFront | Data transfer + HTTPS requests | ~$0.10 |
| SSM Parameter Store | 4 SecureString parameters | ~$0.20 |
| CloudWatch Logs | Lambda log ingestion (14-day retention) | ~$0.10 |

SSM SecureString is the dominant cost — $0.05/parameter/month × 4 parameters = $0.20, plus $0.05 per 10,000 API calls (fetched at each cold start). At typical Lambda cold start rates the call cost is negligible.

All other services are effectively free at personal-use scale. The AWS Free Tier covers Lambda, DynamoDB, and S3 for the first 12 months.

---

## Prerequisites

- AWS account with permissions to create Lambda, DynamoDB, S3, CloudFront, SSM, IAM, and (optionally) Route53 resources
- Terraform ≥ 1.0
- Go ≥ 1.26 (see `go.mod`; this is a release candidate toolchain — download from [go.dev/dl](https://go.dev/dl); for local builds only, CI builds in GitHub Actions)
- GitHub repository (for OIDC-based CI/CD)
- A Google account (for OAuth)

---

## Step 1 — Google OAuth Credentials

You need an OAuth 2.0 Web Application client in Google Cloud Console. You will need the **CloudFront URL** for the redirect URI, but you don't have that yet — you can create the credentials now with a placeholder and update the redirect URI after deploy.

1. Go to [Google Cloud Console → APIs & Services → Credentials](https://console.cloud.google.com/apis/credentials)
2. Click **Create Credentials → OAuth 2.0 Client IDs**
3. Set application type to **Web application**
4. Under **Authorized redirect URIs**, add:
   - `https://<your-cloudfront-domain>/auth/callback` (update this after Step 5 if you don't know it yet)
   - `http://localhost:3000/auth/callback` (for local development)
5. Save. Note your **Client ID** and **Client Secret** — you'll need them in Step 4.

If you plan to use a custom domain, use `https://<your-domain>/auth/callback` instead.

---

## Step 2 — Bootstrap Terraform State

This is a one-time setup that creates the S3 bucket and DynamoDB table used to store Terraform state. Run this once per AWS account.

```bash
cd terraform/bootstrap

terraform init

terraform apply \
  -var="state_bucket_name=notoriousmcp-tfstate-<YOUR_AWS_ACCOUNT_ID>"
```

Note the outputs — you'll need them for the next steps:

```
state_bucket      = "notoriousmcp-tfstate-<YOUR_AWS_ACCOUNT_ID>"
state_lock_table  = "notoriousmcp-tfstate-<YOUR_AWS_ACCOUNT_ID>-lock"
```

---

## Step 3 — Find Your Google Subject ID

The admin bootstrap requires your Google **subject ID** (`sub`) — a numeric string that uniquely identifies your Google account.

**Option A — Google token info endpoint** (easiest, no deploy required):

1. Sign in to any Google-connected app and obtain an access token, or use the [OAuth 2.0 Playground](https://developers.google.com/oauthplayground/) to get one for your account.
2. Call the token info endpoint:
   ```bash
   curl "https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=<your_access_token>"
   ```
3. The `sub` field in the JSON response is your subject ID.

**Option B — after first deploy** (if you skip this step now):

Log in once via `/auth/login`, then scan DynamoDB (user records have `SK = "PROFILE"`):
```bash
aws dynamodb scan --table-name notoriousmcp \
  | jq -r '.Items[] | select(.SK.S == "PROFILE") | {id: .PK.S, email: .Email.S}'
```

The `sub` value looks like `123456789012345678901`. You'll set this as `ADMIN_GOOGLE_IDS` in Step 4.

---

## Step 4 — Set GitHub Secrets

In your GitHub repository, go to **Settings → Secrets and variables → Actions** and add:

| Secret | Value | Where it comes from |
|--------|-------|---------------------|
| `AWS_DEPLOY_ROLE_ARN` | IAM role ARN | `terraform output deploy_role_arn` after first deploy\* |
| `TF_STATE_BUCKET` | S3 bucket name | Step 2 output (`state_bucket`) |
| `GOOGLE_CLIENT_ID` | OAuth client ID | Step 1 |
| `GOOGLE_CLIENT_SECRET` | OAuth client secret | Step 1 |
| `ADMIN_GOOGLE_IDS` | Comma-separated Google subject IDs | Step 3 |
| `TOKEN_SECRET` | Random string ≥ 32 bytes | `openssl rand -base64 32` |
| `REDIRECT_URL` | Full OAuth callback URL | `https://<cloudfront-domain>/auth/callback` (uppercase for GitHub Secrets; Terraform uses the lowercase `redirect_url` var name) |

\* `AWS_DEPLOY_ROLE_ARN` is a chicken-and-egg: the IAM role is created by Terraform, but Terraform needs the role to run in CI. **First deploy:** run Terraform locally (see Step 5), then add the role ARN as a secret so subsequent deploys run via CI.

`TF_STATE_BUCKET` is only the bucket name — the lock table name is derived by appending `-lock` in the deploy workflow's `terraform init` `-backend-config` flags. You don't need a separate secret for it.

**Generating TOKEN_SECRET:**
```bash
openssl rand -base64 32
```

---

## Step 5 — Deploy

### First deploy (local)

Run Terraform locally for the first time to create all AWS resources including the IAM role for CI.

**Tip:** `terraform/terraform.tfvars.example` contains a fully-commented variable template. Copy it to `terraform/terraform.tfvars` and fill it in instead of passing `-var` flags on every apply.

Generate a `TOKEN_SECRET` first and save it — you'll need to reuse the same value on subsequent applies, or existing sessions will be invalidated:

```bash
export TF_VAR_token_secret=$(openssl rand -base64 32)
echo "$TF_VAR_token_secret"   # save this; add as TOKEN_SECRET GitHub secret (without the TF_VAR_token_secret= prefix)
```

Set your AWS region if it's not already in your environment:

```bash
export AWS_DEFAULT_REGION=us-east-1
```

Initialize the backend:

```bash
cd terraform

terraform init \
  -backend-config="bucket=notoriousmcp-tfstate-<YOUR_AWS_ACCOUNT_ID>" \
  -backend-config="dynamodb_table=notoriousmcp-tfstate-<YOUR_AWS_ACCOUNT_ID>-lock" \
  -backend-config="region=us-east-1"
```

**OIDC provider caveat:** Terraform creates a GitHub OIDC provider in your account. If one already exists (e.g. from another project), `terraform apply` will fail with a duplicate resource error. Import it before applying:

```bash
terraform import aws_iam_openid_connect_provider.github \
  arn:aws:iam::<YOUR_AWS_ACCOUNT_ID>:oidc-provider/token.actions.githubusercontent.com
```

> **Note:** The `redirect_url` below uses a placeholder because you don't have the CloudFront URL yet. You'll re-apply with the real URL after the first deploy.

```bash
terraform apply \
  -var="google_client_id=<CLIENT_ID>" \
  -var="google_client_secret=<CLIENT_SECRET>" \
  -var="admin_google_ids=<YOUR_GOOGLE_SUB>" \
  -var="redirect_url=https://PLACEHOLDER.cloudfront.net/auth/callback"
  # TF_VAR_token_secret is picked up from the env var set above
```

After apply, note the outputs:

```bash
terraform output cloudfront_url   # e.g. https://d1234abcd.cloudfront.net
terraform output deploy_role_arn  # e.g. arn:aws:iam::123456789012:role/notoriousmcp-deploy
```

Then:
1. Update your Google OAuth redirect URI in Google Cloud Console to use the real CloudFront URL
2. Re-apply with the real `redirect_url` (reuse the same `TF_VAR_token_secret`):
   ```bash
   terraform apply \
     -var="google_client_id=<CLIENT_ID>" \
     -var="google_client_secret=<CLIENT_SECRET>" \
     -var="admin_google_ids=<YOUR_GOOGLE_SUB>" \
     -var="redirect_url=https://<your-cloudfront-domain>/auth/callback"
     # TF_VAR_token_secret is picked up from the env var set above
   ```
3. Add `deploy_role_arn` as the `AWS_DEPLOY_ROLE_ARN` GitHub secret (Step 4)
4. Add `REDIRECT_URL` and `TOKEN_SECRET` GitHub secrets with the real values

### Subsequent deploys (CI/CD)

Push to `main` — GitHub Actions builds the ARM64 Lambda binary, runs `terraform apply`, and updates the function code automatically.

### Optional: Custom Domain

To use a custom domain instead of the CloudFront URL, add domain variables to your `terraform apply`. All `domain_contact_*` fields are required when `domain_name` is set:

```bash
terraform apply \
  -var="google_client_id=<CLIENT_ID>" \
  -var="google_client_secret=<CLIENT_SECRET>" \
  -var="admin_google_ids=<YOUR_GOOGLE_SUB>" \
  -var="redirect_url=https://<your-domain>/auth/callback" \
  -var="domain_name=notoriousmcp.example.com" \
  -var="domain_contact_first_name=Jane" \
  -var="domain_contact_last_name=Smith" \
  -var="domain_contact_address=123 Main St" \
  -var="domain_contact_city=San Francisco" \
  -var="domain_contact_state=CA" \
  -var="domain_contact_zip=94105" \
  -var="domain_contact_country_code=US" \
  -var="domain_contact_phone=+1.5555550100" \
  -var="domain_contact_email=you@example.com"
  # TF_VAR_token_secret is picked up from the env var set above
```

This registers the domain via Route53 and wires ACM + CloudFront automatically. Update your `REDIRECT_URL` GitHub secret and Google OAuth redirect URI to use the custom domain.

---

## Step 6 — Approve Your Account

After deploy, visit `https://<your-cloudfront-domain>/auth/login` and sign in with Google. Your account is created as **pending**.

If your Google subject ID was in `ADMIN_GOOGLE_IDS`, you're automatically set to **admin** on login — no approval needed. Skip to Step 7.

Otherwise, an existing admin must promote you. If you're setting up for the first time and have no admin yet, use the AWS CLI to update your user record directly:

```bash
aws dynamodb update-item \
  --table-name notoriousmcp \
  --key '{"PK": {"S": "USER#<your-google-sub>"}, "SK": {"S": "PROFILE"}}' \
  --update-expression "SET #s = :s" \
  --expression-attribute-names '{"#s": "Status"}' \
  --expression-attribute-values '{":s": {"S": "admin"}}'
```

Once you have admin access, you can manage other users via MCP tools (Step 7):

```
list_users(status="pending")
update_user(user_id="<google-sub>", status="user")
```

---

## Step 7 — Connect an MCP Client

### Claude Code (recommended)

Run once to register the server:

```bash
claude mcp add --transport http --scope user --callback-port 54321 notoriousmcp https://<your-cloudfront-domain>/mcp
```

Then open `/mcp` in Claude Code, select **notoriousmcp → Authenticate**, and complete the Google OAuth flow in the browser that opens. Claude Code handles registration, token exchange, and auto-refresh automatically.

### Other MCP clients

Any MCP client that supports RFC 7591 dynamic client registration and OAuth 2.0 authorization code flow can connect. Point it at:

- **MCP endpoint:** `https://<your-cloudfront-domain>/mcp`
- **Auth server discovery:** `https://<your-cloudfront-domain>/.well-known/oauth-authorization-server`
- **Client registration:** `POST https://<your-cloudfront-domain>/register`

### Manual token (scripting/testing)

1. Visit `https://<your-cloudfront-domain>/auth/login` in a browser
2. Complete the Google OAuth flow — you'll land on your redirect URI with `?code=<exchange-code>` in the URL
3. POST the exchange code:
   ```bash
   curl -X POST https://<your-cloudfront-domain>/auth/token \
     -H "Content-Type: application/json" \
     -d '{"code": "<exchange-code>", "redirect_uri": "https://<your-cloudfront-domain>/auth/callback"}'
   ```
4. Use the returned token as a static Bearer header in your client config

Tokens expire after 1 hour. The server issues a fresh token via `X-New-Token` on any authenticated request made with an expired-but-otherwise-valid token — clients that handle this header auto-refresh transparently.

To load the skill file (teaches the LLM how to use the tools effectively), add it to `.claude/settings.json` (project) or `~/.claude/settings.json` (global):

```json
{
  "skills": ["path/to/notoriousmcp/skill/notoriousmcp.md"]
}
```

---

## Local Development

```bash
# Copy and fill in env vars
cp .env.example .env

# Start the local server (http://localhost:3000)
# depends_on in docker-compose.yml starts DynamoDB Local and MinIO automatically
docker compose up server
```

Key `.env` values for local dev:

```bash
GOOGLE_CLIENT_ID=<from Google Cloud Console>
GOOGLE_CLIENT_SECRET=<from Google Cloud Console>
REDIRECT_URL=http://localhost:3000/auth/callback
TOKEN_SECRET=<any string ≥ 32 bytes>
ADMIN_GOOGLE_IDS=<your Google subject ID>
```

Add `http://localhost:3000/auth/callback` as an authorized redirect URI in your Google OAuth app (Step 1).

**Run tests:**

```bash
# Unit tests only (integration tests are skipped when DYNAMODB_ENDPOINT is unset)
go test ./...

# Integration tests (requires running docker compose services)
# These env vars match the defaults in docker-compose.yml — source them or inline:
DYNAMODB_ENDPOINT=http://localhost:8000 \
TABLE_NAME=notoriousmcp \
S3_ENDPOINT=http://localhost:9000 \
S3_BUCKET=notoriousmcp-local \
AWS_ACCESS_KEY_ID=localuser \
AWS_SECRET_ACCESS_KEY=localpassword \
AWS_DEFAULT_REGION=us-east-1 \
go test ./...
```

---

## User Management

User roles and what they can do:

| Status | Tools available |
|--------|----------------|
| `pending` | `check_status` only |
| `user` | Notes, todo lists, todos, files (14 tools) |
| `admin` | All user tools + `list_users`, `update_user` (16 tools) |
| `banned` | `check_status` only |

**Admin operations** (requires admin role):

```
list_users()                        → all users
list_users(status="pending")        → pending users only
update_user(user_id=<sub>, status="user")   → approve a user
update_user(user_id=<sub>, status="admin")  → promote to admin
update_user(user_id=<sub>, status="banned") → ban a user
```

`user_id` is the Google subject ID (`sub`) — visible in `list_users` output.

Admins in `ADMIN_GOOGLE_IDS` are forcibly set to admin on every login, so admin status for bootstrap users cannot be accidentally revoked.

---

## Skill File

`skill/notoriousmcp.md` contains a compact tool reference and usage patterns for LLM clients. Load it in Claude Code to teach the model how to use all tools, when to pass `version` for conflict-safe writes, and how to use the files namespace as cross-session memory.
