# Create Workflow Reference

Full step detail and templates for the Create Workflow (creating a new document or
documentation item). The SKILL.md section carries the six-step summary; this file
holds the templates and per-step detail.

## Step 1: Determine Category

Ask the user or infer from context. Use the classification guide:

- Has a deadline or specific goal? → **Projects**
- Ongoing responsibility, no end date? → **Areas**
- Reference material for future use? → **Resources**

If uncertain, load `references/para-categories.md` for the detailed classification
flowchart.

## Step 2: Determine Structure

**Simple item** (single topic, standalone):
→ One document directly in the area: `<owner>/<repo>/<area>/<name>.md`.

**Complex item** (multiple deliverables, ongoing outputs):
→ A group of documents under one `kebab-case/` segment.

## Step 3: Choose Filename

Address: `<owner>/<repo>/<area>/<name>.md` — the store's three coordinates.
`<owner>`/`<repo>` come from the git remote (never chosen by hand); pick `shared` as
the repo for knowledge that is not tied to one codebase. `<area>` is the PARA folder.

Naming convention: `kebab-case.md`

- Use descriptive, lowercase names with hyphens.
- Include a date prefix for time-sensitive items: `YYYY-MM-DD-topic-name.md`.
- For groups: `kebab-case/<document-name>.md`.

Examples:

- `acme/webapp/projects/website-redesign/requirements.md`
- `acme/shared/areas/team-onboarding.md`
- `acme/api/resources/api-reference.md`
- `acme/webapp/projects/website-redesign/2024-03-15-kickoff-notes.md`

Before choosing a new name, `brain_search` the topic: if the store already has a
document on it, this is an edit (`brain_patch`), not a new document.

## Step 4: Write Content

Write through the store — `brain_put` with `path`, `body` and `note`
(`dryRun: true` first for a new document or a large replacement). The `note` is
not optional politeness: it lands in the revision history, which is
what replaces `git log` now that the brain is not a git tree. A write with no note
leaves a change nobody can explain later.

The store also holds non-markdown **text** documents (`.dbml` today) when the brain is
their canonical home; those keep their own syntax and take their title from the
filename. Everything below is about markdown documents.

Use plain markdown. No frontmatter required. Start with an H1 title. Never use
horizontal rules (`---`).

**Simple document template:**

```markdown
# [Title]

[Content starts here]
```

**Group of documents:**

```
acme/webapp/projects/website-redesign/
├── requirements.md
├── design-spec.md
├── 2024-03-15-kickoff-notes.md
└── 2024-03-20-review-notes.md
```

**Meeting notes template:**

```markdown
# [Meeting Title] — YYYY-MM-DD

## Attendees

- [Names]

## Agenda

1. [Topic]

## Notes

[Notes here]

## Action Items

- [ ] [Action item with owner]
```

## Step 5: Connect (Networked Knowledge)

Right after creating a document, wire it into the network so it does not stay an
orphan. Follow `references/linking-rules.md`.

1. **Secure an inbound link**: make the new document receive at least one link from
   a related existing document or the curating MOC. Orphan nodes get lost. Each
   such edit is a `brain_patch` on the linking document.
2. **Contextual links**: weave `[[filename]]` wikilinks naturally into the prose.
   Do not dump a "related links" list at the bottom.
3. **The MOC** — add a one-line link only where a MOC already curates that
   group (a repo hub, a conventions index). Do not create or grow a MOC just to
   make a new document discoverable: search is the discoverability layer, a hub
   line changes no search result, and an inbound MOC link never clears a weak
   node anyway (`linking-rules.md`). What does the job is step 1 +
   `brain_integrity`.
4. **Ground references**: if it is a `resources/` document, also link it to an
   `areas/`/`projects/` document holding your interpretation, grounding it in the
   network.

## Step 6: Confirm and Report

After creation, report:

- Full store path of the created document
- Which PARA category it was placed in
- Brief summary of what was created
- Which document/MOC now links to it (connection status)
