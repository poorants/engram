# engram example brain

A small, complete brain used two ways: as the corpus the search bench measures
against, and as a worked example of what documents in an engram store actually
look like.

Everything here is real knowledge about engram itself, laid out the way the
store expects — PARA areas below a document root, MOC hubs per folder, and
contextual `[[wikilink]]` edges woven into the prose rather than listed at the
bottom.

- [Projects](projects/README.md) — work with an end
- [Areas](areas/README.md) — ongoing responsibilities
- [Resources](resources/README.md) — reference knowledge
- [Archives](archives/README.md) — finished, kept for the record

Load it into a store with:

```bash
python bin/import_tree.py bench/corpus --owner acme --repo shared \
    --url http://localhost:8081 --token "$ENGRAM_TOKEN"
```
