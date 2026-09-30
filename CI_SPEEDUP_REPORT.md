# CI speedup report

Branch: `ci-speedup`, from `origin/beta` at 11dcf79. One workflow exists:
`.github/workflows/release.yml` ("Release builds"), with three jobs, Windows,
Android and Flatpak (Flatpak is on local beta, not yet on `origin/beta`, so it
is not in this branch's copy of the file).

## 1. What happened

| # | Change | File | Before | After | Status |
|---|--------|------|--------|-------|--------|
| 1 | `paths-ignore` for `**.md`, `LICENSE`, `NOTICE` on the push trigger (b4acf30) | `.github/workflows/release.yml` (the `on:` block only) | a doc-only push to beta runs Android, 1.4 to 1.7 min (measured) | a doc-only push runs nothing, 0 min (inference) | **unverified** |

Why unverified (evidence): the workflow triggers on pushes to `main` and
`beta` only, so pushing `ci-speedup` runs nothing, and `workflow_dispatch`
ignores path filters, so a manual run cannot show the skip. Confirming it
needs a doc-only push to `beta`, which this task was not allowed to make.

### Baseline (measured with `gh run view`, runs from 2026-09-30)

Total wall time per run type:

| Run | Jobs that ran | Total |
|-----|---------------|-------|
| push to beta (36690377847, 36687145512) | Android; Windows skipped by design | 1.4 min, 1.7 min |
| tag v0.8.1 (36687145342) | Windows 20.8 min, Android 2.4 min, in parallel | 20.9 min |
| tag v0.8.0 (36685607242) | Windows 20.9 min, Android 3.3 min, in parallel | 20.9 min |
| push to main at a release (36687145211) | Windows, Android | about 22.5 min (from run start and end) |

Slowest 5 jobs (measured): Windows on v0.8.1 20.8 min, Windows on v0.8.0 20.9
min, Windows on main about 22 min, Android on v0.8.0 3.3 min, Android on v0.8.1
2.4 min.

Slowest 5 steps (measured):

| Step | Job | Time |
|------|-----|------|
| Build | Windows (v0.8.1) | 1087 s |
| Build | Windows (v0.8.0) | 1079 s |
| Set up MSYS2 with GTK4 and libadwaita | Windows | 93 to 100 s |
| Build the app (Gradle) | Android | 29 to 74 s |
| Build the Go core for Android | Android | 21 to 66 s |

## 2. What it means

Measured time saved so far: none. The one change is unverified.

Expected saving (inference): about 1.5 min of runner time per doc-only push to
`beta` or `main`. In the last 150 non-merge commits on `beta`, 11 (7%) touched
only `.md`, `LICENSE` or `NOTICE` (evidence: `git log`). Pushes usually carry
several commits, so fewer than 7% of pushes would be skipped (inference). This
saves runner minutes, not time on the release path: every release tag still
builds everything.

## 3. What is risky

- **A doc-only push to `beta` produces no new Android artifact.** The artifact
  would be identical to the previous one, since no built file changed
  (inference). Nothing in the repo consumes beta artifacts automatically
  (evidence: the attach steps run only on tags).
- **Path filters and tags.** GitHub documents that path filters are not
  evaluated for tag pushes, so tags always build. This is from GitHub's
  documentation, not observed on this repo (evidence from docs, unverified
  here).
- **Files the build reads.** No `.md`, `LICENSE` or `NOTICE` file is compiled
  into the app (evidence: the only `go:embed` directives are
  `assets/style.css` and `internal/app/icons/*.svg`). `WHATSNEW.md` is read by
  the "Take this version's notes" steps, which only run on tags, where the
  filter does not apply.
- **The main-branch cache.** The workflow's own comment says `main` builds
  exist to warm the cache for tag builds. A doc-only merge to `main` would now
  not warm it. That does not matter, because a doc-only change leaves the cache
  valid (inference).

## 4. Skipped items

Every job in this repo is a release job: each builds a signed, published
artifact and attaches it to the GitHub release on tags. The rules forbid
editing release jobs, so all job-level items were skipped:

- `timeout-minutes` on jobs: skipped (release jobs). Suggestion: about 45 min
  for Windows (it takes 21 min cold) and 15 min for Android and Flatpak.
- Caching build output and setup actions with caches: skipped (release jobs).
  `setup-go` and `gradle/actions/setup-gradle` already cache (evidence: YAML).
- Concurrency with `cancel-in-progress`: skipped. There are no PR workflows,
  and the rules forbid it on release workflows. `release.yml` already cancels
  superseded runs on branches but never on tags (evidence: YAML).
- Push versus pull_request duplication: does not apply (no `pull_request`
  trigger).
- Linters and formatters first, nightly integration tests: do not apply. CI runs
  no tests or linters at all (evidence: YAML).
- Parallel jobs: already parallel (no `needs:`).
- Matrix and matrix trimming, QEMU and Docker: do not apply.
- Larger runners: suggestion only. The Windows `Build` step (about 18 min,
  compiling gotk4's cgo bindings under MSYS2) dominates every release. A larger
  Windows runner would likely shorten it (speculation, not measured).
- **Release process, no YAML change (the biggest win, inference):** the
  workflow comment says tag runs can only restore caches saved on `main`. For
  v0.8.0 and v0.8.1, `main` and the tag were pushed together, so the tag's
  Windows build ran cold (evidence: both took about 18 min in `Build`, and both
  runs started within a second of each other). Pushing `main`, waiting for its
  run to finish, then pushing the tag should let the tag restore a warm cache
  (inference from the workflow's own comment; not measured).
- Worth considering, out of scope for speed: CI never runs `go test` or
  `go vet`. A small PR or beta workflow for tests would catch failures earlier.

## 5. How to verify

1. Merge `ci-speedup` into `beta` (or cherry-pick b4acf30).
2. Push a commit to `beta` that only edits `README.md`:
   `git commit --allow-empty` does not work for this, because an empty commit
   changes no paths. Edit a line in README.md, commit, push.
3. Check that no run started: `gh run list --branch beta --limit 3`.
4. Push a commit that touches a `.go` file and check that the run does start.
5. Push the next release tag and check that all jobs run:
   `gh run list --limit 3` then `gh run view <id>`.
6. Job and step times for any run:
   `gh run view <id> --json jobs --template '{{range .jobs}}{{.name}} {{.startedAt}} {{.completedAt}}{{"\n"}}{{end}}'`

## Final result

- **Stop condition:** 2, nothing left. No other item on the list applies to
  this repo and is allowed by the hard limits.
- **Starting total time (measured):** 1.4 to 1.7 min for a beta push, 20.9 min
  for a release tag.
- **Ending total time:** unchanged for code pushes and tags (inference: no job
  changed). 0 min for doc-only pushes (inference, unverified).
- **Percent saved (measured):** 0%. Nothing has been confirmed by a real run.
- **Changes made:** 1 of the 15-change cap, and it is unverified.
