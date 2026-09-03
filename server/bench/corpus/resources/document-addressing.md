# How a document is addressed

Every document in the store has one address, and it carries three coordinates:

```
<owner>/<repo>/<area>/<name>.md
  acme / webapp / resources / logging.md
```

`owner` and `repo` are the document root. They are stored as COLUMNS, not just
parsed out of the path, because a query filters on them and because
reclassifying a document would otherwise change its address and break every link
pointing at it. The area is the first segment below the document root and must
be one of the four described in [[para-areas]].

## The depth rule is a ceiling, not a floor

At most `MAX_DEPTH` (5) segments may sit below the document root. Depth is
counted from the document root, so `acme/webapp/areas/backend/architecture/x.md`
is four levels, not six — owner and repo are coordinates, not directory levels.

Writing this rule as a MINIMUM segment count is the tempting mistake and it is
wrong in a way that is easy to miss: it rejects `acme/webapp/README.md`, the repo
hub MOC, which the store indexes and serves happily. A store with that bug has
every repo hub unreadable and unwritable through its tools while the web viewer
shows them fine.

The ceiling exists so the deep folder tree of a file vault cannot grow back.
Depth is replaced by links and MOC hubs — see [[linking-rules]].

## Choosing the repo coordinate

The one-line test is **which code does this knowledge age with?**

- Knowledge that goes stale when one repo's code changes — its conventions, its
  troubleshooting, its build traps — takes that repo's coordinate.
- Knowledge that must outlive any one repo — contracts between repos, error code
  tables, protocol specs, manuals, team conventions — goes to
  `<owner>/shared/`.

Contracts are *always* shared: two copies in two repos have already diverged.
When genuinely unsure, prefer the repo coordinate and promote later with a move,
which leaves an alias behind ([[revisions-and-aliases]]). Promotion is cheap;
copies are expensive.

The owner coordinate is never chosen at all — it is derived from the git remote,
which is what makes it a boundary rather than a habit ([[scope-boundary]]).
