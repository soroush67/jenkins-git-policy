# git-policy-config

GitOps source of truth for the git-policy engine (server-side Git push policy).

| File | Applied to | How |
|---|---|---|
| `test/policy.yaml` | TEST GitLab | automatically by Jenkins job `git-policy-gitops` after each push to `main` |
| `production/policy.yaml` | PRODUCTION GitLab | Jenkins job `git-policy`, `ACTION=UPDATE_POLICY`, `ENVIRONMENT=PRODUCTION` — needs a second person's approval |

Rules for changes:

1. Change through a merge request (protected `main`).
2. Always increase `metadata.revision`; the server refuses equal or lower revisions.
3. Validate locally: `git-policy validate test/policy.yaml`.
4. After merge, check `git-policy-gitops` in Jenkins; promote to PRODUCTION when TEST looks right.
