# NotoriousMCP

A self-hosted MCP (Model Context Protocol) server for notes, todo lists, files, and LLM memory — backed by AWS Lambda + DynamoDB + S3. Named after Notorious B.I.G.

**Tools available:** notes, todo lists, todos, files, user admin, status check  
**Auth:** Google OAuth 2.0 → opaque Bearer tokens (1h TTL, auto-refreshed)  
**IaC:** Terraform with GitHub Actions OIDC CI/CD  
**License:** MIT

---

## Table of Contents

- [Architecture](#architecture)
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
CloudFront  (OAC — signs requests with AWS_IAM)
    │
    ▼
Lambda Function URL  (AWS_IAM auth, response streaming)
    │
    ├── DynamoDB  (single table — users, notes, todos, files, auth codes)
    ├── S3        (note and file content; metadata lives in DynamoDB)
    └── SSM       (secrets fetched at cold start — never in env vars)
```

**Auth flow:**
1. Client visits `/auth/login` → redirected to Google
2. Google redirects to `/auth/callback` with an authorization code
3. Server exchanges the code for Google tokens, creates/updates a user record in DynamoDB
4. Client POSTs the exchange code to `/auth/token` → receives an opaque Bearer token (1h TTL)
5. On expiry, server issues a new token via `X-New-Token` response header — clients auto-refresh

New accounts start as **pending** and can only call `check_status`. An admin must promote them to **user** or **admin**.

**Admin bootstrap:** The `ADMIN_GOOGLE_IDS` SSM parameter holds a comma-separated list of Google subject IDs that are forcibly set to `admin` status on every login. This self-heals if admin status is accidentally removed.

---

## Prerequisites

- AWS account with permissions to create Lambda, DynamoDB, S3, CloudFront, SSM, IAM, and (optionally) Route53 resources
- Terraform ≥ 1.0
- Go ≥ 1.21 (for local builds)
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

The admin bootstrap requires your Google **subject ID** (`sub`) — a numeric string that uniquely identifies your Google account. You can retrieve it after your first login (Step 6), but it's easier to get it now:

```bash
# Option A: decode an ID token from Google's token endpoint
# Option B: use the Google OAuth2 token info endpoint:
#   https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=<your_access_token>
# Option C: after first deploy, log in once, then check DynamoDB:
#   aws dynamodb scan --table-name notoriousmcp | jq '.Items[] | select(.SK.S == "USER")'
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
| `REDIRECT_URL` | Full OAuth callback URL | `https://<cloudfront-domain>/auth/callback` |

\* `AWS_DEPLOY_ROLE_ARN` is a chicken-and-egg: the IAM role is created by Terraform, but Terraform needs the role to run in CI. **First deploy:** run Terraform locally (see Step 5), then add the role ARN as a secret so subsequent deploys run via CI.

**Generating TOKEN_SECRET:**
```bash
openssl rand -base64 32
```

---

## Step 5 — Deploy

### First deploy (local)

Run Terraform locally for the first time to create all AWS resources including the IAM role for CI.

```bash
cd terraform

terraform init \
  -backend-config="bucket=notoriousmcp-tfstate-<YOUR_ACCOUNT_ID>" \
  -backend-config="dynamodb_table=notoriousmcp-tfstate-<YOUR_ACCOUNT_ID>-lock" \
  -backend-config="region=us-east-1"

terraform apply \
  -var="google_client_id=<CLIENT_ID>" \
  -var="google_client_secret=<CLIENT_SECRET>" \
  -var="admin_google_ids=<YOUR_GOOGLE_SUB>" \
  -var="token_secret=$(openssl rand -base64 32)" \
  -var="redirect_url=https://PLACEHOLDER.cloudfront.net/auth/callback"
```

After apply, note the outputs:

```bash
terraform output cloudfront_url   # e.g. https://d1234abcd.cloudfront.net
terraform output deploy_role_arn  # e.g. arn:aws:iam::123456789:role/notoriousmcp-deploy
```

Then:
1. Update your Google OAuth redirect URI in Google Cloud Console to use the real CloudFront URL
2. Re-run `terraform apply` with the real `redirect_url`
3. Add `deploy_role_arn` as the `AWS_DEPLOY_ROLE_ARN` GitHub secret (Step 4)
4. Update `REDIRECT_URL` GitHub secret with the real CloudFront URL

### Subsequent deploys (CI/CD)

Push to `main` — GitHub Actions builds the ARM64 Lambda binary, runs `terraform apply`, and updates the function code automatically.

### Optional: Custom Domain

To use a custom domain instead of the CloudFront URL, add domain variables to your `terraform apply`:

```bash
terraform apply \
  -var="domain_name=notoriousmcp.example.com" \
  -var="domain_contact_first_name=Jane" \
  -var="domain_contact_last_name=Smith" \
  -var="domain_contact_address=123 Main St" \
  -var="domain_contact_city=San Francisco" \
  -var="domain_contact_state=CA" \
  -var="domain_contact_zip=94105" \
  -var="domain_contact_country_code=US" \
  -var="domain_contact_phone=+1.5555550100" \
  -var="domain_contact_email=you@example.com" \
  # ... other required vars
```

This registers the domain via Route53 and wires ACM + CloudFront automatically. Update your `REDIRECT_URL` and Google OAuth redirect URI to use the custom domain.

---

## Step 6 — Approve Your Account

After deploy, visit `https://<your-cloudfront-domain>/auth/login` and sign in with Google. Your account is created as **pending**.

If your Google subject ID was in `ADMIN_GOOGLE_IDS`, you're automatically set to **admin** on login — no approval needed.

Otherwise, an existing admin must approve you. Connect an MCP client (Step 7), then:

```
update_user(user_id="<your-google-sub>", status="user")
```

To find pending users:

```
list_users(status="pending")
```

---

## Step 7 — Connect an MCP Client

Add to your MCP client config (e.g. Claude Code's `.claude/settings.json`):

```json
{
  "mcpServers": {
    "notoriousmcp": {
      "type": "http",
      "url": "https://<your-cloudfront-domain>/mcp",
      "headers": {
        "Authorization": "Bearer <your-token>"
      }
    }
  }
}
```

To get a token:
1. Visit `https://<your-cloudfront-domain>/auth/login` in a browser
2. Complete the Google OAuth flow — you'll be redirected back with a short-lived exchange code
3. POST the exchange code to get a Bearer token:
   ```bash
   curl -X POST https://<your-cloudfront-domain>/auth/token \
     -H "Content-Type: application/json" \
     -d '{"code": "<exchange-code>", "redirect_uri": "https://<your-cloudfront-domain>/auth/callback"}'
   ```
4. Use the returned token in your MCP client config

Tokens expire after 1 hour. When a token expires, the server issues a new one via the `X-New-Token` response header — compatible MCP clients handle this automatically.

To load the skill file (teaches the LLM how to use the tools effectively):

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

# Start DynamoDB Local and MinIO
docker compose up -d dynamodb minio

# Run the local server (http://localhost:3000)
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
# Unit tests only
go test ./...

# Integration tests (requires running docker compose services)
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
