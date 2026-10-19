# ADR-0014: Managed import collections reconcile via an external manifest

## Status

Proposed (2026-10-19)

## Context

The importer (`internal/importer`) is one-shot: a zip upload creates pages
that do not exist and skips everything that does. The same manual is
re-imported weekly, so users need reconciliation — update changed pages,
delete pages whose source disappeared, never touch pages outside the
collection — with one safety rule: a page that was edited in the wiki and
also changed upstream is a conflict, and a conflicted batch applies zero
writes. Re-runs must be idempotent and must converge to the same result when
a process is killed mid-execution.

That requires remembering, per imported source file, which page it produced
and what content was last applied. Two properties constrain where that state
can live:

- **Markdown files are the source of truth; SQLite stores are derived and
  rebuildable** ([ADR-0001](./0001-filesystem-source-of-truth.md)). Page
  files already round-trip `leafwiki_*` system frontmatter plus arbitrary
  extra fields.
- Extra frontmatter fields are not durable ownership storage. The UI write
  path (`NodeStore.UpsertContentAndMetadata`) replaces editor-owned string
  properties and tags on every edit, so a string marker written into a page
  can be removed by ordinary editing precisely in the "someone edited this
  imported page" scenario the feature exists to detect.

The importer already persists cross-run state outside the wiki tree in
`<storageDir>/.importer/` (`current-plan.json`, extracted workspaces), and
page identity (`leafwiki_id`) is stable across tree reconstruction.

## Decision

A managed import is an explicit mode (`mode=managed`) bound to a **named
collection** pinned to one target base path. Reconciliation state lives in a
JSON manifest per collection at:

```
<storageDir>/.importer/managed/<collection>.json
```

Concretely:

- `CollectionStore` (`internal/importer/manifest_store.go`) owns the
  directory. Collection names validate against `[a-z0-9-]{1,64}` (lowercase
  only, so manifest filenames cannot collide on case-insensitive
  filesystems); writes are temp-file-plus-rename atomic, mirroring
  `PlanStore`.
- Each entry maps the normalized bundle-relative source path to
  `{target_path, page_id, kind, title, applied_fingerprint, generation}`.
  Two collections may not pin base paths that segment-prefix-overlap.
- `applied_fingerprint` is a SHA-256 over canonicalized
  title + frontmatter-extras + body, with local asset references normalized
  to `asset://sha256/<hash>` tokens on both the bundle and the live-page
  side. `leafwiki_*` fields (including pinning) do not participate.
- The manifest doubles as the crash-recovery journal: execution start bumps
  a `generation` and clears `completed_keys` (idempotent when the same plan
  resumes); each item is journaled after its page write; finish clears the
  journal. The planner stays read-only and performs zero uploads; asset
  uploads dedupe by content hash so replaying an item after a crash never
  creates a second file.
- A planning pass reports one of create/update/skip/delete plus machine
  conflict codes (`local-modified`, `unmanaged-occupant`, `managed-missing`,
  `delete-drifted`, `section-has-children`, …). Any blocking conflict sets
  plan `blocked`, and execution is refused before the first write. Pages
  never recorded for the collection cannot be updated or deleted by it.
- Legacy import is the default strategy and keeps its behavior and JSON
  shape byte-identical; mode/collection fields are `omitempty`.

Retiring a collection is an operator action: delete its manifest file.

## Alternatives considered

- **Ownership marker in page frontmatter**: rejected — the UI metadata write
  path replaces string extra fields, so the marker disappears exactly when
  a page is hand-edited, which is when conflict detection must work.
- **A SQLite ownership table**: rejected — it would make importer state a
  second source of truth that reconstruction/backup semantics do not cover,
  against ADR-0001. The manifest is plain JSON next to the existing importer
  state and survives on the same backup paths as `current-plan.json`.
- **Git/history-based drift detection via revisions**: rejected — the
  revision store does not tell us what the importer itself wrote, adds
  history-shape assumptions, and comparing against the latest revision still
  cannot distinguish "importer applied X" from "user saved X"; an explicit
  applied fingerprint is simpler and exact.
- **Force-override option on conflict**: deferred. v1 reports conflicts and
  the user resolves them by reverting the page or changing the source; an
  explicit "accept source" path can be added later without changing the
  manifest format.
- **Recursive delete of removed sections**: rejected — a section may contain
  pages created manually in the wiki after import. Managed deletes are
  non-recursive; a section that would still contain children after the batch
  is a `section-has-children` conflict.
- **Fingerprint of fully rewritten content including asset URLs**: rejected
  — uploaded asset filenames depend on collision suffixes and the page ID,
  making bundle-side planning non-deterministic and forcing uploads during a
  read-only planning pass. Hash tokens make the fingerprint independent of
  the on-disk asset name.

## Consequences

- One small JSON file per collection, written once per item during
  execution; planning reads all manifests (few, small) to detect occupants
  owned by other collections.
- Changes made directly to page files outside the application are invisible
  to the tree until resync, but the fingerprint reads raw page content at
  plan time, so such edits are still detected as drift.
- Manifest loss degrades gracefully to "nothing is managed": pages are never
  deleted or overwritten on the basis of a missing manifest — at worst a
  path appears occupied and is reported as `unmanaged-occupant`.
- v1 limitations, revisitable without a format break: page↔section kind
  flips (`x.md` ↔ `x/index.md`) conflict instead of converting; empty
  intermediate sections created implicitly have no entries and are never
  swept; an empty managed bundle is rejected rather than deleting the
  collection; there is no collection-management HTTP API.
- Cross-process concurrency stays unsupported (single-binary/single-plan
  assumption already held by `PlanStore`); in-process access is mutex
  serialized.
