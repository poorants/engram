# PARA Review Checklist

## Review Procedure

The store has no listing tool. Enumerate from the hubs — `brain_get
<owner>/<repo>/README.md` and each area's MOC, following links and backlinks —
plus `brain_integrity` (orphans are exactly the documents no hub reaches).
"Last modified" is the newest entry of `brain_revisions <path>`.

### 1. Projects Review

For each item in `<owner>/<repo>/projects/`:

- [ ] Is this project still active? (Last modified within 30 days)
- [ ] Does it have a clear goal or deadline?
- [ ] Are there any completed deliverables that should be archived?
- [ ] Is the project's hub document up to date?

**Archive if**: Project is completed, cancelled, or inactive for >30 days.

### 2. Areas Review

For each item in `<owner>/<repo>/areas/`:

- [ ] Is this still an ongoing responsibility?
- [ ] Is the documentation current and accurate?
- [ ] Are there any items that have become project-specific?
- [ ] Does the content reflect current practices?

**Archive if**: Responsibility has ended or been transferred.

### 3. Resources Review

For each item in `<owner>/<repo>/resources/`:

- [ ] Is the information still accurate and relevant?
- [ ] Has this been superseded by newer material?
- [ ] Is it referenced by any active projects or areas?
- [ ] Does it need updates to reflect current tools/practices?

**Archive if**: Content is outdated or superseded.

## Archive Criteria

An item is an archive candidate when ANY of these conditions are met:

| Condition | Applies to |
|-----------|-----------|
| Last modified >30 days ago with no recent references | Projects |
| All tasks/checklist items are completed | Projects |
| Project was explicitly cancelled | Projects |
| Responsibility has been transferred or ended | Areas |
| Content has been superseded by newer material | Resources |
| Referenced tools or APIs are deprecated | Resources |

## Review Report Format

```markdown
## PARA Review Report — YYYY-MM-DD

### Archive Candidates
Items recommended for archiving (requires user confirmation):
- [ ] acme/<repo>/projects/item.md — [reason]
- [ ] acme/<repo>/projects/item.md — [reason]

### Needs Update
Items with potentially outdated content:
- acme/<repo>/<area>/item.md — [what needs updating]

### Recently Archived
Items archived since last review:
- acme/<repo>/archives/item.md — archived on YYYY-MM-DD

### Summary
| Category  | Items | Archive Candidates | Needs Update |
|-----------|-------|--------------------|--------------|
| Projects  | X     | Y                  | Z            |
| Areas     | X     | Y                  | Z            |
| Resources | X     | Y                  | Z            |
| Archives  | X     | —                  | —            |
```

## Review Frequency

- **Projects**: Review weekly or at sprint boundaries
- **Areas**: Review monthly
- **Resources**: Review quarterly
- **Full PARA review**: At least once per quarter
