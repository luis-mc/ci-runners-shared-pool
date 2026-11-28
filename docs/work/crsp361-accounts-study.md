# CRSP361 study: connecting a person's own accounts (AWS, GitHub, others)

Status: research only, 2026-11-28. Not a spec. Nothing here was run against a cloud account.
Scope: how a self-hosted crsp controller (home PC or LAN, no cloud identity) should connect the owner's GitHub and AWS accounts, and later other providers, from the web console only.

Sources marked (verified) were read on 2026-11-28 from the primary page. Items marked (from memory, verify) were not re-read. The AWS and GitHub doc pages carry no date; they are living docs. Nothing relied on is known to be older than 2 years, except where flagged.

## 0. Summary of recommendations

| Provider | Chosen method | Main reason |
|---|---|---|
| AWS | IAM Roles Anywhere with a controller-made private CA, set up by one CloudFormation quick-create link; static IAM user keys only as a labelled fallback | Short-lived credentials from a machine with no AWS identity; no key ever pasted; least-privilege role created in the user's own account |
| GitHub | Keep the per-controller GitHub App via the manifest flow (R5.2). Use JIT runner config for every ephemeral runner (all overflow). For persistent local runners keep registration tokens, but add a registration reconcile loop | App tokens are short-lived and scoped; JIT configs are single-use; reconcile fixes the restart loop |
| GCP | Workload Identity Federation with an X.509 or OIDC provider, later | Same shape as AWS Roles Anywhere |
| Azure | App registration with a certificate credential (federated credential needs a public OIDC issuer) | Home PC has no public issuer |
| Hetzner, DigitalOcean | Project-scoped API token pasted once, stored in OS secret store | No federation exists; token is the only method |

## 1. What is different about crsp

1. Self-hosted and open source: the project owns no AWS account and no servers. So the usual SaaS pattern (the vendor's AWS account is the trusted principal in a cross-account role with an external ID) has no vendor account to point at. Every controller would need its own broker account. Rejected (see 2.2).
2. The controller runs on a home PC: no instance profile, no IRSA, no metadata service. It needs a credential it can hold itself.
3. The owner rule: the web console does everything. The user may be sent to the provider's own page only for steps the provider forbids us to do (signing in, accepting a consent screen, pressing "Create stack"). Everything else (generating keys, checking the result, showing what was granted) is done by the controller.
4. The existing GitHub spec already follows the right shape: one App per controller, manifest flow, secrets in the OS store, no PAT (docs/features/github-integration/spec.md sections 2, 3, R5.2, R5.23). Section 3 below only extends it.

## 2. AWS

### 2.1 Options compared

| Option | Credential lifetime | Works from a home PC | Setup by the user | Verdict |
|---|---|---|---|---|
| IAM Roles Anywhere (X.509) | 15 min to 12 h, you choose (verified: CreateSession durationSeconds 900 to 43200) | Yes, designed for workloads outside AWS | One stack launch | Chosen |
| IAM user access keys | Long-lived until rotated | Yes | Create user, policy, key, paste | Fallback only |
| Cross-account role + external ID | 1 h default | Only if a product-owned AWS account exists | Role with trust on the vendor account | Rejected for crsp |
| IAM Identity Center (SSO) | 1 to 12 h, but needs a person to sign in with a browser when it expires | Poorly: unattended controller cannot renew the sign-in | Many steps, org feature | Rejected for unattended use |
| STS web identity from a GitHub or own OIDC issuer | Short | Needs a public HTTPS issuer URL; a home PC has none | IdP registration | Rejected for AWS; same limit applies to Azure (verified: federated credentials trust an external IdP's OIDC issuer) |

### 2.2 Why Roles Anywhere

- AWS says it gives "temporary security credentials ... for workloads such as servers, containers, and applications that run outside of AWS" and removes long-term keys (verified).
- It needs an X.509 certificate chain: the user's CA is registered as a trust anchor, the workload signs a CreateSession request with its certificate key and gets AssumeRole credentials. All Roles Anywhere resources are regional (verified). The trust boundary is the account, so the role trust policy must pin the trust anchor ARN and ideally the certificate subject (verified for trust anchor via `aws:SourceArn`).
- crsp can be its own tiny CA: at connect time the controller creates a P-256 or RSA CA keypair, a CA certificate, and a leaf (client) certificate for the controller. The CA public certificate goes to AWS as the trust anchor. Private keys never leave the PC.
- Leaf rotation needs no AWS action: the controller re-issues the leaf from its own CA (for example every 90 days, CA valid 5 years). Only a CA renewal needs the stack updated (the console shows a one-click "Update stack" link with the new CA certificate).
- Cost: Roles Anywhere itself has no charge (from memory, verify at the pricing page before publishing UI text).
- Signing: CreateSession is not in the AWS SDKs; AWS ships `aws_signing_helper` (credential-process or serve mode) (verified that CreateSession has no native SDK support). Plan: the controller vendors the Go implementation of the signing step (AWS publishes the helper as open source, `aws/rolesanywhere-credential-helper`, from memory, verify licence and pin a version) and caches credentials until about 5 minutes before expiry. Ask for 1 h sessions.
- Why not a pure cross-account role: AWS's own guide says a third party must supply its own AWS account ID and an external ID that the third party generates (verified). There is no such account in a self-hosted product. The external-ID idea still teaches one thing we copy: bind the role to a value only this controller can present. Here that value is the certificate subject (for example `CN=crsp-<controller-id>`), checked in the role trust policy with a condition on the Roles Anywhere source identity.

### 2.3 One-click CloudFormation quick-create link

Format (verified, AWS docs):

```
https://<region>.console.aws.amazon.com/cloudformation/home?region=<region>#/stacks/create/review
  ?templateURL=<URL-encoded https S3 URL of the template>
  &stackName=crsp-overflow
  &param_ControllerId=<id>
  &param_TrustAnchorCertPem=<...>   (see note)
  &param_AllowedRegions=eu-west-1
  &param_MonthlyBudgetUsd=50
  &param_AlertEmail=<optional>
```

Facts that shape the design:
- `templateURL` must be an S3 URL (verified). The project hosts the versioned template in a public S3 bucket (or any public bucket the maintainers own). The template is public, so no secret in it. The console also offers "View the template" and a copy button, and states its hash, so a user can read what it will create (CORE-3, CORE-4).
- Parameters marked `NoEcho` are ignored in the URL (verified); nothing secret goes in the link. The CA certificate is public, so it may go in as a parameter. A PEM is long (about 700 chars) but fits in a URL; if a browser limit bites, put the CA certificate in the controller's own HTTPS URL and let the template read it... that would need a public URL, so rejected; instead have the user paste it (the console shows a Copy button). Flag as a prototype question (section 6).
- The user must tick "I acknowledge that this template may create IAM resources" (CAPABILITY_NAMED_IAM, verified that IAM-creating templates need the acknowledgement). This is a human step the console cannot do for them; the screen says so before the link opens.
- The controller cannot read the stack output without credentials. Instead, the user pastes one value back: the role ARN (the console tells them where it is on the Outputs tab), or better, the template outputs a single "connection string" `role=...;anchor=...;profile=...;region=...` with a Copy button, pasted into one box. The controller then calls the test (2.6).

Template resources (sketch, names illustrative):
- `AWS::RolesAnywhere::TrustAnchor` with source type CERTIFICATE_BUNDLE and the CA PEM (from memory, verify property names).
- `AWS::RolesAnywhere::Profile` with a session policy equal to the permission policy (belt and braces) and `DurationSeconds: 3600`.
- `AWS::IAM::Role` trusting `rolesanywhere.amazonaws.com` (actions `sts:AssumeRole`, `sts:TagSession`, `sts:SetSourceIdentity`) with condition `ArnEquals aws:SourceArn = <trust anchor ARN>` and `StringEquals aws:PrincipalTag/x509Subject/CN = crsp-<ControllerId>` (from memory, verify the tag key syntax).
- The network pieces for runners (a dedicated VPC, a subnet, a security group with no inbound rules, an instance profile for the runner instance if it needs one). Prefer creating these in the stack so the role needs no CreateVpc rights and every launch can be pinned to them.
- `AWS::Budgets::Budget` (monthly cost, alert at 80 percent forecast and 100 percent actual) with an optional email subscriber, so the guard exists even if the controller is off. Budget data lags 8 to 12 hours and can overshoot (verified), so it is a second line, not the first.

### 2.4 Permission policy sketch (the role)

Principles: only the actions crsp performs; instances only in the named regions; launch only with the crsp tag; terminate and stop only instances that carry that tag; tags cannot be rewritten to escape the guard (AWS warns about this, verified: deny tag edits on tag-protected resources). Describe actions cannot be resource-restricted (verified), so they are in a separate statement; they can still be limited by region.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "ReadOnlyDescribe",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeInstances", "ec2:DescribeInstanceStatus", "ec2:DescribeImages",
        "ec2:DescribeSubnets", "ec2:DescribeSecurityGroups", "ec2:DescribeVpcs",
        "ec2:DescribeInstanceTypes", "ec2:DescribeInstanceTypeOfferings",
        "ec2:DescribeVolumes", "ec2:DescribeTags", "ec2:DescribeLaunchTemplates",
        "ec2:DescribeLaunchTemplateVersions"
      ],
      "Resource": "*",
      "Condition": { "StringEquals": { "aws:RequestedRegion": ["${AllowedRegion}"] } }
    },
    {
      "Sid": "LaunchOnlyIntoCrspNetworkWithCrspTag",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": [
        "arn:aws:ec2:${AllowedRegion}:${AccountId}:instance/*",
        "arn:aws:ec2:${AllowedRegion}:${AccountId}:volume/*",
        "arn:aws:ec2:${AllowedRegion}:${AccountId}:network-interface/*"
      ],
      "Condition": {
        "StringEquals": {
          "aws:RequestTag/crsp-controller": "${ControllerId}",
          "aws:RequestedRegion": "${AllowedRegion}"
        },
        "ForAllValues:StringEquals": { "aws:TagKeys": ["crsp-controller", "crsp-job", "Name"] },
        "StringLikeIfExists": { "ec2:InstanceType": ["c7a.*", "c7i.*", "m7a.*", "m7i.*", "t3.*"] }
      }
    },
    {
      "Sid": "LaunchUsesOnlyTheCrspSubnetGroupAndAmi",
      "Effect": "Allow",
      "Action": "ec2:RunInstances",
      "Resource": [
        "arn:aws:ec2:${AllowedRegion}:${AccountId}:subnet/${SubnetId}",
        "arn:aws:ec2:${AllowedRegion}:${AccountId}:security-group/${SecurityGroupId}",
        "arn:aws:ec2:${AllowedRegion}::image/*"
      ]
    },
    {
      "Sid": "TagOnlyAtLaunch",
      "Effect": "Allow",
      "Action": "ec2:CreateTags",
      "Resource": "arn:aws:ec2:${AllowedRegion}:${AccountId}:*/*",
      "Condition": { "StringEquals": { "ec2:CreateAction": "RunInstances" } }
    },
    {
      "Sid": "StopAndTerminateOnlyCrspInstances",
      "Effect": "Allow",
      "Action": ["ec2:TerminateInstances", "ec2:StopInstances", "ec2:StartInstances"],
      "Resource": "arn:aws:ec2:${AllowedRegion}:${AccountId}:instance/*",
      "Condition": { "StringEquals": { "ec2:ResourceTag/crsp-controller": "${ControllerId}" } }
    },
    {
      "Sid": "NeverRewriteTheGuardTag",
      "Effect": "Deny",
      "Action": ["ec2:CreateTags", "ec2:DeleteTags"],
      "Resource": "*",
      "Condition": {
        "ForAnyValue:StringEquals": { "aws:TagKeys": ["crsp-controller"] },
        "StringNotEquals": { "ec2:CreateAction": "RunInstances" }
      }
    },
    {
      "Sid": "PassOnlyTheRunnerInstanceProfileRole",
      "Effect": "Allow",
      "Action": "iam:PassRole",
      "Resource": "arn:aws:iam::${AccountId}:role/crsp-runner-instance",
      "Condition": { "StringEquals": { "iam:PassedToService": "ec2.amazonaws.com" } }
    }
  ]
}
```

Notes on the sketch:
- RunInstances touches several resource types, each must be allowed or the call fails (verified in EC2 docs: a policy with a condition key must name the resource that key applies to, otherwise use `...IfExists`). Hence the split into three statements. Expect to refine with a dry run (2.6).
- Instance type allow-list as a cost guard (`ec2:InstanceType` is a RunInstances key; from memory, verify). Make the list a stack parameter.
- Region restriction uses `aws:RequestedRegion` (all EC2 actions support it; verified).
- The runner's own AMI, user data, spot, and an optional launch template are decisions for the overflow spec; add `ec2:RunInstances` on `launch-template/*` if a template is used.
- Remove `iam:PassRole` and the instance profile if the runner instances need no AWS access (the common case: they only call GitHub and the controller).
- Do not give `ec2:*`, `iam:*`, or `AdministratorAccess` in any case, even in the fallback IAM-user path.

### 2.5 Fallback: IAM user access keys

Allowed for people who cannot use Roles Anywhere (for example an AWS Organizations SCP blocks it). The console shows a second tab "Use an access key (less safe)": a separate stack creating only the IAM user, the same permission policy, and an access key delivered how? CloudFormation cannot output a secret safely (a `AWS::IAM::AccessKey` secret appears in stack outputs; risky). So the fallback is: stack creates the user with the same policy; user creates the key in the IAM page and pastes it once. Stored in the OS store, shown only as "AKIA...XXXX, created N days ago", with a rotation reminder at 90 days and a "Rotate" button that creates the new key via the old key then deletes the old (needs `iam:CreateAccessKey`/`DeleteAccessKey` on self only, `arn:aws:iam::*:user/${aws:username}`). Mark as less safe and say why (CORE-3, plain words).

### 2.6 Verify the connection ("Test connection")

Run in this order, show each as a line with a status glyph and word (UX-58):
1. Sign a CreateSession and get credentials (proves the trust anchor, role and certificate match).
2. `sts:GetCallerIdentity` (proves the credentials work; show account ID and role name; it needs no IAM permission).
3. `ec2:DescribeInstances` in each allowed region (proves read).
4. `ec2:RunInstances` with `DryRun=true` with the exact tags, subnet, group and AMI the controller will use. `DryRunOperation` means allowed, `UnauthorizedOperation` means denied (verified in EC2 docs). Decode failures with `sts:DecodeAuthorizationMessage` (needs that permission; add it).
5. `ec2:TerminateInstances` with `DryRun=true` against a fake or an existing tagged instance, to prove the tag condition.
6. A negative check: a dry-run launch without the tag must be denied. This proves the guard works and shows the owner the role is not too broad. Show "Denied as expected".
7. Read back the budget (needs `budgets:ViewBudget`) and show "Budget 50 USD per month exists" or warn if missing.

IAM changes take time to propagate (EC2 docs advise about 5 minutes, verified), so step 4 retries with backoff for up to 2 minutes before showing a failure.

### 2.7 Storing and rotating secrets locally

- Private keys (CA and leaf) and any fallback access key go in the OS secret store the controller already uses (decision 0002, github-integration R5.x secrets); on Windows that is DPAPI, on macOS Keychain, on Linux Secret Service or an encrypted file with a machine-bound key. Never in config files, logs, test fixtures (AGENTS.md secret scan; build planted secrets at run time).
- Show: "Certificate expires 2027-02-20", "Credentials fetched 14:05". Never show the private key. "Reveal" does not exist for keys.
- Rotation: leaf cert automatic at 30 days before expiry (no AWS action); CA by guided stack update; fallback key by Rotate. Session credentials live in memory only.
- Backups: if the PC is lost, the user deletes the stack (or the trust anchor) in AWS; the console's Disconnect screen (section 5) shows that exact link. A leaked leaf cert can also be revoked by importing a certificate revocation list (CRL) into the trust anchor (from memory, verify) or by changing the role trust subject condition.

### 2.8 Cost guards (defence in depth)

1. Client-side hard caps (first line, instant): maximum concurrent EC2 runners, maximum hours per instance, monthly spend estimate from instance-type price times hours; refuse new launches above the cap. These live in crsp settings.
2. Time to live on every instance: tag `crsp-expires-at`; a reaper loop in the controller and a second reaper via an `AWS::Events` rule plus Lambda is out of scope; instead set instance-initiated shutdown behaviour to terminate and have the runner image run `shutdown -h +N` as a dead-man switch (from memory, a common pattern in ec2-github-runner style setups; verify).
3. Sweep: on every start, list instances with the controller tag and terminate unknown or expired ones.
4. AWS Budgets in the stack (2.3): alert only by default; Budget Actions can attach a deny IAM policy at a threshold (verified: budget actions can apply a custom IAM policy that denies provisioning). Offer as an advanced toggle.
5. Instance type allow-list and region list (policy, 2.4).
6. Service quotas: the account's own On-Demand vCPU quota is a hard ceiling; show it ("Your account allows 32 vCPUs"), read via `servicequotas:GetServiceQuota` or `ec2:DescribeAccountAttributes`.

## 3. GitHub

### 3.1 App creation: keep the manifest flow

- The manifest flow (verified): the controller posts a manifest to `https://github.com/settings/apps/new` (or `/organizations/<org>/settings/apps/new`); GitHub redirects back with a temporary `code`; the controller calls `POST /app-manifests/{code}/conversions` and receives `id`, `pem`, `webhook_secret` (and client secret). All three steps must finish within one hour; the endpoint is rate limited; the user who completes the flow owns the App. This is already built (R5.2). No change recommended except to name the owner type: personal account or organization, because an organization App needs an organization owner.
- Installation scope: on install the user picks "Only select repositories" or all. Recommend default text on the install screen: select only the repositories that will use the pool; the App can be widened later without a new App (R5.17, R5.18 already list installations).
- Webhook: stay off by default, polling plus the optional real-webhook switch (R5.5, R5.51).

### 3.2 Least permissions (verified table)

For the self-hosted runner endpoints (registration token, remove token, generate-jitconfig, delete runner) GitHub requires: repository permission Administration (write) for repository-level runners, or organization permission Self-hosted runners (write) for organization-level runners. List runners needs read. Both accept installation tokens (verified). So the current R5.23 table is right, and one mapping is worth saying in the console: "Administration (repository) is the only way to manage runners for a single repository; if every runner is organization-level, the App needs only Self-hosted runners (organization) and not Administration." Offer two profiles in the Review step:
- Organization pool: Self-hosted runners (org, write), Actions (read), Metadata (read). No Administration.
- Repository pool: Administration (repo, write) plus the same.
The Variables (read and write) and Contents (read) permissions are for the `CRSP_RUNS_ON_*` flip and the `runs-on` check, so they stay only if those features are on. Administration is broad (it includes branch protection and repo settings, from memory, verify) so the profile that avoids it is a real least-privilege win when the owner uses an organization.

### 3.3 JIT config versus registration token

| | JIT (generate-jitconfig) | Registration token |
|---|---|---|
| Runner | Ephemeral, one job, registered on the fly | Persistent or ephemeral |
| Secret | `encoded_jit_config`, passed once to the runner | Token expires after one hour (verified) |
| Required body | name, runner_group_id, labels (at least 1), optional work_folder (verified) | none |
| App permission | Administration write (repo) or Self-hosted runners write (org) (verified) | same |
| Cleanup | GitHub removes the ephemeral runner after its job; a never-started one is removed after 1 day offline (verified) | You must deregister with a remove token; stale ones vanish after 14 days offline (verified) |

Recommendation:
- AWS EC2 overflow, and any machine that is created for one job: JIT only. The controller fetches the config right before launch, passes it to the instance through user data or SSM (user data is readable from the instance; the config is single-use and the instance is short-lived, acceptable; encrypt or prefer SSM Parameter Store SecureString with the role limited to that one parameter if a stronger guard is wanted). philips-labs does the same: JIT on by default for ephemeral runners, and its housekeeper deletes stored tokens and JIT configs older than 1 day (verified).
- Persistent local runners: registration token on demand (already R5.31), plus the reconcile loop below.
- Runner groups: JIT needs `runner_group_id`; default group is id 1 for organizations; for repository-level runners the call uses the repository's default group (from memory, verify). Show the group in the Review step.

### 3.4 The restart loop on deleted registrations (current bug)

Facts: GitHub removes a self-hosted runner automatically after it has not connected for more than 14 days; an ephemeral runner after 1 day (verified). The runner process on the PC still has its local credentials, tries to connect, GitHub answers that the registration is gone, the service exits, and the supervisor restarts it, forever.

Recommended controller behaviour:
1. Before any restart, ask GitHub: `GET .../actions/runners` and match by the runner's name or id (requires only read access). If absent, the registration is gone: mark the runner "registration lost" (a status word and glyph, UX-58), do not restart.
2. Recovery: stop the service, remove local configuration, fetch a fresh registration token (or, for ephemeral, a new JIT config), re-register with `--replace`, start. Do it once automatically; if the second try fails, stop and show the reason (UX-6 no dead ends) with a "Register again" button.
3. Restart limit: a runner process that exits fast more than N times in M minutes enters a back-off (1, 2, 4 ... 30 min), never a tight loop; the reason is shown.
4. Prevent: a machine that is off for more than 14 days will always lose its persistent runners; at start the controller sweeps and re-registers rather than assuming. Optionally keep one keep-alive job-less check (the runner connects every few seconds while up, so a PC that is on is safe).
5. Org-wide: use the offline-cleanup sweep that exists (R5.33) only for registrations crsp created (label `crsp-<controller-id>`), never for others.
6. Prefer ephemeral for anything new: no stale registration can exist longer than a day.

## 4. Generic pattern for "Connect provider X"

What generalises across AWS, GCP, Azure, Hetzner, DigitalOcean, GitHub (reviewed: Terraform Cloud dynamic credentials, Spacelift, Vercel, Grafana Cloud integration flows are all the same shape; I did not re-read each in this pass, treat as from memory and verify before quoting):

1. Choose the safest method the provider offers, in this order: federation or role (no stored secret), certificate or app credential (stored key, short sessions), API token (stored secret, scoped), password (never).
2. The product creates as much as it can in the provider and in its own store (keys, certificates), and gives the user one provider link that pre-fills a template. The user's job is only to sign in and approve.
3. The product shows exactly what will be created and what it can do, in plain words, before the link opens (UX-30 Review).
4. One paste back, or none (a connection string).
5. A Test step that proves each capability with a harmless call and a negative test.
6. A permanent "What this connection can do" card, generated from the real policy, with a date and a "Re-check" button.
7. Disconnect: a button that first stops using the credential, deletes local secrets, and shows the provider link to delete the created stack or token; it never silently leaves access behind. Also "Revoke now" for the emergency.
8. Health: the connection shows ok, expiring, or broken with the reason (credential expired, role removed, budget exceeded), checked on a timer.

Provider notes:
- GCP: Workload Identity Federation with a pool, a provider (X.509 certificate, or OIDC/SAML/AWS), and a service account impersonation; credential configuration file, no key file (from memory, the cloud.google.com page redirected to docs.cloud.google.com and was not read). Same CA trick as AWS if X.509 providers are available; otherwise a service account key as fallback with a rotation reminder.
- Azure: federated credentials need a public issuer URL and exact issuer, subject and audience match (verified), so a home PC cannot use them directly. Use an app registration with a certificate credential (the controller keeps the key) or a client secret with expiry. The sign-in uses the device-code flow so the console can create the app registration with the user's own consent. Least role: a custom role limited to a resource group.
- Hetzner Cloud and DigitalOcean: no federation; a project-scoped token (Hetzner tokens are per project, read or read/write; DigitalOcean supports custom scopes, from memory, verify). Paste once, store in the OS store, show the last 4 characters, test with a read call and a labelled server create/delete, rotate with a new paste.
- All: one provider interface in the controller (connect, test, describe-grants, disconnect, health) so the console screens are shared (CORE-1, CORE-2).

Open-source references for the AWS pattern: RunsOn installs as one CloudFormation stack from a launch link, then registers its GitHub App from the stack output (verified, runs-on.com install guide). philips-labs terraform-aws-github-runner keeps the App ID, key and webhook secret in SSM, supports KMS, ephemeral runners with JIT, and a scale-down schedule (verified); it does not describe removing stale GitHub registrations (verified: the housekeeper only removes stored tokens and JIT configs). machulav/ec2-github-runner and Cirun use access keys or a cross-account role (from memory, not re-read).

## 5. Console flow (numbered screens)

All screens use the wizard of UX-5 (step bar on top, one primary action, Back at the bottom) and, because they change something, end with Review, work, Done (UX-30). Words go in `content` (checklist). Rules cited by ID; the highest design-system folder is v4.

Connect AWS (Overflow targets > Add AWS):
1. Why and what you need: one headline, three plain lines ("You need an AWS account. We create a role that can only start and stop crsp runners. It costs nothing to connect."). Primary: Start. CORE-3, CORE-5, CORE-6.
2. Choose the region(s), the monthly budget, the maximum runners, the instance types (rows up to 6, drop-down for more, UX-22). Defaults filled. Primary: Next. CORE-4.
3. Review (UX-30): lists what will be created in the user's AWS (role, trust anchor, profile, network, budget) and what the role can do, as plain sentences generated from the policy, with a link "Read the template". Says "Nothing has changed yet." Primary: "Create the key and open AWS". CORE-5, CORE-4.
4. Work: the controller makes the CA and certificate, then shows "Open AWS CloudFormation" (new tab) with instructions "Tick the box about IAM, press Create stack, wait for CREATE_COMPLETE, copy the connection string". Live waiting status (UX-58). Stop is present and withdraws nothing remote. Paste box with Verify. CORE-6, UX-38 (primary disabled with its reason until the string parses), UX-6.
5. Test connection: the seven checks in 2.6 as rows with glyph and word; retry on propagation delay; failure shows the exact AWS message in plain words and the next step. CORE-3, CORE-5, UX-6.
6. Done: shows account ID (last 4), regions, role name, budget, certificate expiry, and the button "Run a test job on AWS" (optional, costs cents). Done after reload still shows the result (wizard states list in AGENTS.md checklist).

Manage (after connect): a card "AWS, connected" with a status word, what it can do, last check time, and actions Re-check, Rotate, Disconnect. Cards in one row share size (CORE-7). Disconnect is a question card (UX-9): Cancel on the left, "Disconnect AWS" on the right, listing what stops, then a Done step with the link to delete the stack.

GitHub (First run wizard, existing): add two choices to the Review step: runner profile (organization or repository) and the repository selection hint. Add a "Runner health" section: shows "registration lost" and the one button "Register again". CORE-4, UX-6.

## 6. Risks and open questions

1. A public S3 bucket for the template is project infrastructure that must stay up and must never be tampered with; pin versions by hash and show the hash in the console. A takeover of the bucket would let an attacker create a broad role, so the console must show the template digest and the maintainers must sign it (decision needed: who owns the bucket).
2. Roles Anywhere property names, trust tag keys and the CRL option must be checked against the current CloudFormation reference before building; they are from memory here.
3. Quick-create URL length with an embedded PEM; fall back to a paste step if it fails.
4. The role cannot create VPC resources in the sketch; the stack must create them. Many users have default VPCs and would prefer them. Choose: create a new isolated VPC (recommended, no inbound rules) or accept a VPC id parameter.
5. User data carries the JIT config; anyone with `ec2:DescribeInstanceAttribute` on the instance can read it. The config is single-use and short-lived; accept or move to SSM.
6. Budgets lag 8 to 12 hours (verified), so only the client-side caps and the dead-man shutdown protect against a runaway within a day; test the reaper before shipping.
7. An AWS Organizations SCP may block Roles Anywhere or IAM creation; the failure must say so (UX-6) and offer the fallback.
8. A lost PC: leaf certificate theft gives a thief credentials limited to the role (tagged instances, region list, instance types), still billable. Document the "delete the stack" emergency step on the Disconnect screen.
9. GitHub: the Administration permission is broad; the organization profile avoids it but only works if every runner is organization-level (needs the runner-pool spec to confirm).
10. GitHub deletes only registrations; the local `.runner` files remain; the reconcile loop must also clean those.
11. Older-than-2-years flags: none of the cited pages show a date, so age is unknown; the CloudFormation quick-create and external ID guidance have existed since before 2024 and are stable (from memory). The Microsoft page shows `ms.date 2025-04-09`, updated 2026-08-13 (verified).

## 7. Sources

Verified in this session:
- GitHub, removing self-hosted runners (14 days offline, ephemeral 1 day): https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/removing-self-hosted-runners
- GitHub REST, self-hosted runners (generate-jitconfig body, registration token expiry): https://docs.github.com/en/rest/actions/self-hosted-runners
- GitHub REST, permissions required for GitHub Apps (Administration vs Self-hosted runners): https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps
- GitHub, registering an App from a manifest: https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest
- AWS CloudFormation quick-create links: https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/cfn-console-create-stacks-quick-create-links.html
- AWS IAM, third-party access and external ID: https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_create_for-user_externalid.html
- AWS IAM Roles Anywhere introduction: https://docs.aws.amazon.com/rolesanywhere/latest/userguide/introduction.html
- AWS IAM Roles Anywhere CreateSession (duration 900 to 43200 s): https://docs.aws.amazon.com/rolesanywhere/latest/userguide/authentication-create-session.html
- AWS EC2 identity-based policies (Describe not resource-level, ABAC, dry run, IfExists): https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/iam-policies-for-amazon-ec2.html
- AWS Budgets (8 to 12 hour lag, budget actions): https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-managing-costs.html
- Microsoft Entra workload identity federation: https://learn.microsoft.com/en-us/entra/workload-id/workload-identity-federation
- RunsOn install guide (CloudFormation launch plus App registration): https://runs-on.com/guides/install/
- philips-labs terraform-aws-github-runner: https://github.com/philips-labs/terraform-aws-github-runner

Not read in full (from memory, verify before use):
- Google Cloud Workload Identity Federation: https://docs.cloud.google.com/iam/docs/workload-identity-federation (301 redirect from cloud.google.com, not fetched)
- AWS Roles Anywhere credential helper: https://github.com/aws/rolesanywhere-credential-helper
- AWS EC2 service authorization reference (condition keys per action): https://docs.aws.amazon.com/service-authorization/latest/reference/list_amazonec2.html
- machulav/ec2-github-runner: https://github.com/machulav/ec2-github-runner
- Cirun: https://docs.cirun.io
