# Classify & Import Reference

This reference covers the **Classify & Import** workflow: scattered, unclassified
knowledge documents in a repo the store admits → PARA-classified documents in the
store. The repo files are the source; the store is the destination; nothing is
moved on disk.

Only repos the store admits can be imported. A personal repo's documents are
refused (403) and there is no other brain to take them — do not start a plan
for one.

## Procedure (Steps 1–6)

### Step 1: Scan

Discover candidate documents in the working repo with Glob.

- Targets: `**/*.md`, `**/*.txt`
- Exclude non-document directories (`.git/`, `node_modules/`, build output) and
  root metadata files — see Exclusion Patterns below.

### Step 2: Classify

Read each document and decide two things:

- its **area**, with the flowchart in `references/para-categories.md`;
- its **repo coordinate** — this repo, or `shared` — with the routing test in
  SKILL.md (*which code does this knowledge age with?*).

Judge by filename/path hints and content keywords (heuristics below). Mark
uncertain documents as "manual classification needed". Then `brain_search` each
title as a question: a document the store already has is an **update or a
merge**, not a second copy.

### Step 3: Present the import plan

Output the classification as a plan with full store paths. Name collisions with
existing store documents.

```
## Import Plan

### Classified (12 files)
| Source | Store path | Reason |
|--------|------------|--------|
| docs/api-spec.md | acme/api/resources/api-spec.md | Reference material |
| notes/kms-roadmap.md | acme/api/projects/kms-roadmap.md | Active project with deadline |
| notes/error-codes.md | acme/shared/resources/error-codes.md | Contract between repos |

### Already in the store (1 file)
| Source | Store document | Proposal |
|--------|----------------|----------|
| docs/build-traps.md | acme/api/resources/build-traps.md | merge the new section with brain_patch |

### Manual Classification Needed (2 files)
| Source | Notes |
|--------|-------|
| notes/misc.md | Content unclear — user decision needed |

### Skipped (3 files)
| Source | Reason |
|--------|--------|
| README.md | Root metadata file |

### Summary
- Total scanned: 18 files
- Auto-classified: 12 · merges: 1 · manual: 2 · skipped: 3
```

### Step 4: Confirm

**Always execute only after user approval.** The user may change a document's
classification or coordinate, exclude specific files, or specify custom paths.

### Step 5: Execute

For each approved document:

1. Adapt it to the writing rules (Create Workflow step 4):
   an H1, no frontmatter, no
   `---` rules. Turn repo-relative links into store links (`[[name]]` or full
   store paths) where the target is imported too; leave a plain code span where
   it is not.
2. `brain_put` it with `dryRun: true`, then for real, with a `note` naming the
   source (`imported from soha:docs/api-spec.md`).
3. For a merge, `brain_get` the existing document and `brain_patch` the new
   material in.

Whether the repo copies are then deleted is **the user's decision**. A repo doc
may be load-bearing — linked from code comments, CI, a README — so propose it
with that grep, never do it unasked.

### Step 6: Report

Report the result, then run Link & Connect and close with the Integrity Check.

```
## Import Report

### Imported (11 files)
- docs/api-spec.md → acme/api/resources/api-spec.md
- notes/kms-roadmap.md → acme/api/projects/kms-roadmap.md

### Merged (1 file)
- docs/build-traps.md → acme/api/resources/build-traps.md (§ "Windows")

### Skipped (1 file)
- notes/misc.md — user excluded

### Failed (0 files)

### Next Steps
- Link & Connect for the new documents
- repo copies: keep / remove (user's call)
```

A whole local `brain/` or `para/` tree left over from before the store is a
bulk promotion, not a document-by-document import, and an operator job: the
engram repo's `server/bin/import_tree.py` loads a tree under one
`--owner`/`--repo` (it needs the store's ingest credential). Agree the target
coordinates with the user first — it crosses the confidentiality boundary.

## Exclusion Patterns

### Directories to Exclude

| Pattern | Reason |
|---------|--------|
| `.git/` | Version control |
| `node_modules/` | Dependencies |
| `dist/`, `build/`, `out/` | Build output |
| `.vscode/`, `.idea/` | IDE configuration |
| `.claude/`, `.waypoint/` | Claude Code configuration and session state |
| `.next/`, `.nuxt/` | Framework cache |
| `vendor/`, `__pycache__/` | Language-specific dependencies |
| `coverage/` | Test coverage output |

### Root Metadata Files to Exclude

These files serve the repository itself and stay with it:

- `README.md`, `README`
- `LICENSE`, `LICENSE.md`
- `CHANGELOG.md`, `CHANGES.md`
- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `CLAUDE.md`
- `package.json`, `package-lock.json`
- `tsconfig.json`, `pyproject.toml`
- `.gitignore`, `.editorconfig`

### Source Code Documentation to Exclude

Documentation embedded within source code directories stays with the code:

- `src/**/README.md` — code module docs belong with the code
- `lib/**/README.md` — library docs belong with the library
- Files in directories that are primarily code (`src/`, `lib/`, `app/`, `components/`)

Documents with their own external delivery lifecycle (a manual's source, a
generated report) also stay — see "Brain boundary" in SKILL.md.

## Classification Heuristics

### By Filename and Path

| Pattern | Suggested Category | Confidence |
|---------|-------------------|------------|
| `*roadmap*`, `*plan*`, `*sprint*` | Projects | High |
| `*proposal*`, `*rfc*`, `*spec*` | Projects | Medium |
| `*meeting*`, `*minutes*`, `*standup*` | Projects (under related project) | Medium |
| `*guide*`, `*howto*`, `*tutorial*` | Resources | High |
| `*reference*`, `*cheatsheet*`, `*glossary*` | Resources | High |
| `*process*`, `*policy*`, `*standard*` | Areas | High |
| `*onboarding*`, `*runbook*`, `*playbook*` | Areas | Medium |
| `*template*` | Resources | Medium |
| `*adr*`, `*decision*` | Resources | High |
| `*retrospective*`, `*postmortem*` | Projects (or Archives if old) | Medium |

### By Content Keywords

When filenames are not conclusive, scan the first ~50 lines of content:

| Keywords | Suggested Category |
|----------|-------------------|
| "deadline", "due date", "milestone", "Q1/Q2/Q3/Q4" | Projects |
| "TODO", "in progress", "blocked", "sprint" | Projects |
| "policy", "must", "always", "never", "standard" | Areas |
| "responsible for", "ownership", "maintain" | Areas |
| "how to", "step 1", "usage", "example" | Resources |
| "reference", "see also", "documentation" | Resources |

### Confidence Levels

- **High**: Auto-classify with the suggested category
- **Medium**: Auto-classify but flag for user review in the plan
- **Low / Unclear**: Mark as "manual classification needed"

## Grouping

### Single Files

A directory holding one document flattens into one store document:

```
docs/api/overview.md → acme/<repo>/resources/api-overview.md
```

### Related File Groups

Several related documents keep their grouping as a path segment:

```
docs/project-alpha/
├── requirements.md
├── design.md
└── timeline.md

→ acme/<repo>/projects/project-alpha/
  ├── requirements.md
  ├── design.md
  └── timeline.md
```

### Mixed Directories

A directory whose documents belong to different areas is split:

```
docs/
├── api-reference.md    → acme/<repo>/resources/api-reference.md
├── sprint-plan.md      → acme/<repo>/projects/sprint-plan.md
└── coding-standards.md → acme/<repo>/areas/coding-standards.md
```

## Name Conflict Resolution

When the store path already holds a document:

1. **Same content** (identical or near-identical): skip, report as duplicate.
2. **Overlapping content**: propose a merge with `brain_patch`.
3. **Different subject, same name**: pick a more specific name — never a numeric
   suffix; a name is what someone searches for.

Always report conflicts in the plan so the user can decide.

## Edge Cases

| Case | Action |
|------|--------|
| Empty file (0 bytes) | Skip, report in skipped list |
| Binary file | Skip, report in skipped list |
| Symbolic link | Skip, report in skipped list |
| File >10MB | Skip, report in skipped list |
| File without extension | Skip unless `.md` or `.txt` |
| Nested deeper than the store allows (5 levels below the root) | Flatten; flag for user review |
| Same filename in multiple directories | Treat each independently, resolve conflicts per rules above |
