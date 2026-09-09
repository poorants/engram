-- The canonical schema.
--
-- **This file never DROPs.** The store's `docs.body` and `revisions` are the
-- original — there is no copy anywhere else. Dropping them means there is
-- nowhere to restore from.
--
-- Two layers:
--   canonical — docs (body, path, metadata) and revisions (history)
--   derived   — chunks, tsv, links. All of it can be rebuilt from a body at any time.
--
-- Idempotent. Running it again against an existing database touches no data,
-- which is what lets it run on every boot and keeps a migration step out of the
-- deployment sequence.

CREATE TABLE IF NOT EXISTS docs (
  id         serial PRIMARY KEY,
  path       text UNIQUE NOT NULL,       -- acme/webapp/projects/foo.md — the address and the name
  title      text NOT NULL,
  -- The two axes are orthogonal. `area` is *what kind of knowledge*, the
  -- owner/repo pair is *whose it is*. Merged into one column, both are lost.
  area       text NOT NULL,              -- projects | areas | resources | archives
  -- Two of the three coordinates. Derived from the path <owner>/<repo>/<area>/…
  -- but every query reads the COLUMNS: distinguish by path alone and
  -- reclassifying a document changes its address, breaking every link that
  -- pointed at it.
  --
  -- owner is the **confidentiality boundary**. An allow-list of repos falls
  -- behind the moment someone creates one; "this group only" keeps being
  -- correct. That is not a convention — it is enforced as a write refusal.
  owner      text NOT NULL DEFAULT '',
  -- The repo name, or 'shared' for knowledge that belongs to no single repo.
  repo       text NOT NULL DEFAULT 'shared',
  body       text NOT NULL,              -- the canonical markdown
  sha256     text NOT NULL,
  chars      int  NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  -- No hard delete. Removing something from a brain usually means "I do not
  -- look at this any more", not "this never existed", and a wrong delete needs
  -- somewhere to come back from.
  deleted_at timestamptz,
  -- A channel for the title and path alone. What made grep strong was filename
  -- matching, and mixing that signal into the same bag as body lexemes dilutes it.
  tsv        tsvector
);
CREATE INDEX IF NOT EXISTS docs_tsv_idx   ON docs USING gin(tsv);
CREATE INDEX IF NOT EXISTS docs_area_idx  ON docs(area);
CREATE INDEX IF NOT EXISTS docs_owner_idx ON docs(owner, repo);
CREATE INDEX IF NOT EXISTS docs_upd_idx   ON docs(updated_at DESC);
CREATE INDEX IF NOT EXISTS docs_live_idx  ON docs(deleted_at) WHERE deleted_at IS NULL;

-- What git used to do: what changed, when, how — and going back. Every write
-- leaves one row holding the **previous body**. The path is duplicated here so
-- the history survives the document's deletion, and doc_id is cut with SET NULL.
CREATE TABLE IF NOT EXISTS revisions (
  id         bigserial PRIMARY KEY,
  doc_id     int REFERENCES docs(id) ON DELETE SET NULL,
  path       text NOT NULL,
  body       text NOT NULL,
  sha256     text NOT NULL,
  author     text NOT NULL DEFAULT '',   -- who or what wrote it
  note       text NOT NULL DEFAULT '',   -- why it changed
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS rev_doc_idx  ON revisions(doc_id, created_at DESC);
CREATE INDEX IF NOT EXISTS rev_path_idx ON revisions(path, created_at DESC);

-- -- everything below is derived ---------------------------------------------
-- The unit returned by a search is a CHUNK, not a document. Without its
-- heading_path an agent uses a fragment as evidence without knowing where it
-- was cut from.
CREATE TABLE IF NOT EXISTS chunks (
  id           serial PRIMARY KEY,
  doc_id       int NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
  ord          int NOT NULL,
  heading_path text NOT NULL DEFAULT '',
  body         text NOT NULL,
  chars        int  NOT NULL,
  -- Lexemes are built by the indexer rather than left to a parser: identifiers
  -- stay whole (the default parser splits `http_client_pool`), and CJK is
  -- indexed as syllable 2-grams so no extension is required.
  -- setweight: title and headings = A, body = B (ts_rank weighs A 2.5x).
  tsv          tsvector
);
CREATE INDEX IF NOT EXISTS chunks_tsv_idx ON chunks USING gin(tsv);
CREATE INDEX IF NOT EXISTS chunks_doc_idx ON chunks(doc_id);

-- An edge points at a **document id**. In a file vault a link had to name a
-- file, because a file had no other identity; a row in a database has an
-- immutable primary key — so moving a document does not break the edges that
-- already resolved, which is the single biggest property this schema buys.
-- dst_name is kept so a link to a document that does not exist YET is still
-- visible (a broken link) and can be connected later.
--
-- kind exists because a wikilink is a *contextual* connection while a markdown
-- link from a MOC is a *structural* one. Orphan detection must look only at the
-- contextual ones and the viewer must show both; in one bag, neither is possible.
CREATE TABLE IF NOT EXISTS links (
  src      int  NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
  dst_name text NOT NULL,
  dst      int  REFERENCES docs(id) ON DELETE SET NULL,
  kind     text NOT NULL DEFAULT 'wiki'      -- wiki | md
);
ALTER TABLE links ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'wiki';
-- Edges are a set. Naming the same target five times in one body is one edge.
CREATE UNIQUE INDEX IF NOT EXISTS links_uniq ON links(src, dst_name, kind);
CREATE INDEX IF NOT EXISTS links_src_idx ON links(src);
CREATE INDEX IF NOT EXISTS links_dst_idx ON links(dst);

-- The paths a document used to have. **A file vault could never have this.**
-- Move a document and the `[[old name]]` somebody else wrote keeps reaching it;
-- as files, the only options were editing two hundred references or leaving
-- them broken.
CREATE TABLE IF NOT EXISTS aliases (
  path       text PRIMARY KEY,
  doc_id     int  NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS aliases_doc_idx ON aliases(doc_id);

CREATE TABLE IF NOT EXISTS meta (k text PRIMARY KEY, v text NOT NULL);

-- Let an already-created database catch up. The schema file has to be
-- idempotent to run on every boot, and that is what keeps a migration step out
-- of the deployment sequence.
ALTER TABLE docs ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE docs ADD COLUMN IF NOT EXISTS deleted_at timestamptz;
ALTER TABLE docs ADD COLUMN IF NOT EXISTS owner text NOT NULL DEFAULT '';
ALTER TABLE docs ADD COLUMN IF NOT EXISTS repo  text NOT NULL DEFAULT 'shared';

-- -- usage feedback ------------------------------------------------------------
-- What the ranking cannot learn from text alone: whether a document, once
-- found, actually answered. Two tables, both derived-in-spirit (the brain is
-- whole without them) but never rebuilt from anything — a vote that is lost is
-- lost — so they live here with the canonical tables rather than with chunks.
--
-- searches is the query log: what was asked, by whom, at which tier, and what
-- came back. It is what makes a later "this document was useful" attributable
-- to the question that found it, which in turn is what lets search remember
-- an answer for the NEXT time a similar question is asked (the remembered
-- channel in search.py). q_tsv holds the query's lexemes, built by the same
-- function as the index, so a new question can be matched against old ones
-- with the operator the rest of the search already uses.
CREATE TABLE IF NOT EXISTS searches (
  id         bigserial PRIMARY KEY,
  q          text NOT NULL,
  q_tsv      tsvector,
  author     text NOT NULL DEFAULT '',
  tier       int  NOT NULL DEFAULT 0,        -- 0: a plain (untiered) call
  hits       jsonb NOT NULL DEFAULT '[]',    -- [{chunk, doc, path, heading_path, score}] in rank order
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS searches_tsv_idx ON searches USING gin(q_tsv);
CREATE INDEX IF NOT EXISTS searches_at_idx  ON searches(created_at DESC);

-- feedback is one vote per row. kind is one of
--   useful  — this document answered (explicit: brain_feedback, engram feedback, the viewer)
--   noise   — this document was in the way (explicit)
--   opened  — the document was fetched right after a search returned it
--             (implicit; recorded by the store when brain_get names the search)
-- The vote is on the DOCUMENT; chunk_id only says which fragment was in front
-- of the voter, and it goes NULL when the document is re-chunked. search_id
-- ties the vote to the question, and is what the remembered channel joins on.
CREATE TABLE IF NOT EXISTS feedback (
  id         bigserial PRIMARY KEY,
  doc_id     int NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
  chunk_id   int REFERENCES chunks(id) ON DELETE SET NULL,
  search_id  bigint REFERENCES searches(id) ON DELETE SET NULL,
  kind       text NOT NULL,
  author     text NOT NULL DEFAULT '',
  note       text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
-- One vote of a kind per person per document per QUESTION per day. Pressing
-- the button twice is one "useful", and opening a document five times after
-- one search is one "opened" — a vote is a fact about the document, not a
-- counter to run up. But the same document answering two different questions
-- on one day is two facts (the bench caught the version that keyed on the day
-- alone: a document that was the answer to two questions could only be
-- remembered for one of them). A vote with no search is keyed on 0.
-- The day is taken in UTC because the expression has to be immutable to be
-- indexed; which day a vote lands on does not matter, only that a day is a day.
CREATE UNIQUE INDEX IF NOT EXISTS feedback_one_per_question
  ON feedback(doc_id, author, kind, (COALESCE(search_id, 0)), ((timezone('UTC', created_at))::date));
CREATE INDEX IF NOT EXISTS feedback_doc_idx    ON feedback(doc_id);
CREATE INDEX IF NOT EXISTS feedback_search_idx ON feedback(search_id);
