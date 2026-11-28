# CRSP361 report (RELEASE-73): AWS EC2 overflow spec

Status: in progress. Spec only; no code.

## Steps
1. Branch CRSP361-aws-overflow-spec from CRSP383-integration. Read agent-common.md and crsp361-accounts-study.md.
2. Writing docs/features/aws-overflow/spec.md (new feature folder: overflow to AWS is its own feature; runners and github-integration are cross-linked).
3. Spec written: docs/features/aws-overflow/spec.md (status proposed), change note changes/CRSP361.md, index rebuilt. docs_check --strict: 0 errors, 0 warnings.
4. docs.aws.amazon.com was not reachable from the session; AWS facts not re-read are marked (verify) in the spec (Q11).

Template recommendation: option A (crsp hands over the filled stack file; owner uploads it in the CloudFormation console). Quick-create links need S3 (vendor account), so not used.
