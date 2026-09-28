# 0010. Import playback history and close the remaining Jellystat gaps

- **Status:** Accepted
- **Date:** 2026-09-27

## Context

Bloom records playback from the moment a media server is registered
(ADR 0005). Everything that happened before that moment is invisible, and for
an operator replacing Jellystat that is years of history. ADR 0005 item 6
already names the two sources for a backfill: the server's own records and the
Playback Reporting plugin. Real use has now made the gap concrete, and the
operator has asked for everything Jellystat can show, not only the history.

Jellystat's feature inventory is in `docs/research/jellystat.md`. Against it,
Bloom already has: live sessions and history, overall, per-library, per-user,
and own dashboards, play-method and stream analytics with a per-watch series,
roles and viewer accounts, and notifications. It does not yet have:

1. **History import** from the Playback Reporting plugin, from Jellystat
   itself, or from Jellyfin's per-user play state.
2. **A library catalog**: item, season, and episode counts per library, item
   pages with their own history, "recently added", "most popular" (distinct
   viewers) next to "most viewed", genre breakdowns, and stale-media reports.
   All of these need Bloom to know what is in each library, which the collector
   deliberately does not (ADR 0005 keeps no item catalog).
3. **An activity view**: every watch across users, searchable and filterable,
   and the per-user timeline that groups consecutive plays of one title.
4. **Collection settings**: users and libraries to exclude from collection.
5. **Backup and restore** of Bloom's own statistics.

Jellystat's own import has documented defects (a one-shot procedure that
inserts nothing on the second run, and rows skipped when the item is not in
its catalog); see the research notes, gap 2.

## Decision

1. **Imports are a first-class, resumable job.** An import is a row in an
   `imports` table with a source, a media server, a state (`pending`,
   `running`, `completed`, `failed`, `cancelled`), counters (read, imported,
   skipped, duplicate), a bounded last error, and a cursor. A supervised
   worker claims jobs with the same lease pattern as fulfilment and
   notifications, processes them in bounded batches, checkpoints the cursor
   after every batch, and survives a restart by resuming from the cursor.
   Administrators start, watch, and cancel imports from an **Imports** page
   under Administration and through `/api/v1/imports`.

2. **Every imported watch is idempotent by source record.** Imported rows go
   into the existing `watches` table with `source = import`, plus a new
   `import_source` (`playback_reporting`, `jellystat`, `jellyfin_userdata`) and
   `import_record_id` (the plugin `rowid`, the Jellystat activity `Id`, or the
   Jellyfin item id for user data), unique per media server and source. Running
   an import twice changes nothing. A nullable `import_origin_record_id` keeps
   the upstream Playback Reporting `rowid` when Jellystat marks an activity as
   imported; cross-source deduplication uses that origin instead of encoding a
   marker into Jellystat's own activity ID. A record whose item is unknown to
   the media server is still imported with the names the source carries;
   nothing is skipped for lack of a catalog.

3. **Imported watches never double-count collected ones.** An imported record
   that overlaps a watch Bloom collected itself (same media server, media user,
   and item, with start times within the resume window of ADR 0005) is recorded
   as a duplicate and not inserted. Bloom's own collection wins because it has
   segments and samples; the import has one number.

4. **Three sources, in this order.**
   - **Playback Reporting plugin**, through Jellyfin's API
     (`/user_usage_stats/submit_custom_query` against `PlaybackActivity`),
     paged by `rowid`, read-only, with the same safe transport as every other
     media-server call. Fields: date, user, item, item type and name, play
     method, client, device, duration. This is the richest history most
     Jellyfin operators have.
   - **Jellystat**, from a backup file the administrator uploads (the JSONL
     backup Jellystat writes, tables `jf_playback_activity` and, when present,
     `jf_libraries`, `jf_library_items`, `jf_library_seasons`,
     `jf_library_episodes`, `jf_users`). Uploads are bounded in size, parsed as
     a stream, never executed, and discarded after the job completes. A direct
     database connection to Jellystat is not offered: it would be a second
     credential to protect for a one-time job that the backup already serves.
   - **Jellyfin per-user play state** (`UserData.PlayCount`,
     `LastPlayedDate`, `PlaybackPositionTicks`) for every item in every library:
     the coarsest source, one synthetic watch per item and user at the last
     played date, used only where neither richer source covers that user and
     item. It is what remains when nothing else was ever installed.

5. **A library catalog is synchronised, type-agnostically.** Bloom keeps
   `library_items` (id, library, parent, type, name, series and season links,
   index numbers, runtime, premiere date, production year, community rating,
   genres, primary image tag, date created, archived flag) for every item type
   Jellyfin returns under a library, not a fixed list of types. A scheduled
   sync (default hourly, plus on demand) walks each library with paged,
   bounded requests, upserts by item id, and marks items missing from a full
   walk as archived rather than deleting them, so history keeps its names. The
   catalog powers library overviews and counts, item pages with their history,
   recently added, popular against viewed, genres, stale media, and lets
   imports resolve names. Images are never copied; the UI loads them from the
   media server through a signed, expiring proxy route so the API key stays
   server-side.

6. **The activity view and timeline are queries over `watches`**, paged and
   filtered by user, library, item type, client, device, play method, and
   date, with search on the title, and a per-user timeline that groups
   consecutive watches of one title. No new tables.

7. **Exclusions are settings, applied at collection and import.** Excluded
   media users and excluded libraries are stored per media server, editable by
   administrators, and honoured by the collector, every import source, and the
   catalog sync. Existing rows are not deleted when an exclusion is added; the
   dashboards filter them out.

8. **Backup is Bloom's job, not a feature.** Bloom's data is one database; the
   operator backs up the database. Bloom adds a documented export of watches
   and imports as JSONL (`GET /api/v1/exports/watches`, streamed, bounded per
   page) so data can leave, and accepts its own export as a fourth import
   source, which is also how a move between SQLite and PostgreSQL works.

9. **Slicing.** Slice one: import job model, worker, Imports page, Playback
   Reporting source, Bloom export and re-import. Slice two: Jellystat backup
   source. Slice three: library catalog sync with library overview, item pages,
   recently added, popular, genres, stale media, image proxy. Slice four:
   activity view and timeline, exclusions. Jellyfin user-data import comes with
   the catalog (it walks the same items). Each slice ships thin and end to end
   with its UI, per the agreed review process.

## Consequences

### Good

- History arrives without the defects Jellystat users report: re-running an
  import is safe, unknown items are kept, and progress survives a restart.
- Collected and imported data live in one table with one `source` column, so
  every existing dashboard includes history at once, and accuracy questions
  stay answerable.
- The catalog is the one new model; everything else in the Jellystat inventory
  becomes a query or a page over data Bloom already has.

### Bad

- The catalog is a large table for big libraries and a scheduled walk of the
  media server. Bounds, paging, and the hourly default keep it cheap; it
  remains the most expensive thing Bloom does.
- Imported watches have one duration and no segments or samples, so the
  per-watch series and pause accounting are empty for them; the UI says so.
- Jellyfin per-user play state is lossy by nature (one synthetic watch per
  item and user); it is clearly labelled and used last.

### Alternatives considered

- **Jellystat's approach** (stage plugin rows, copy with a procedure): rejected
  for the one-shot and skipped-item defects noted in the research.
- **Connecting to Jellystat's database directly**: rejected in favour of the
  backup file; same data, no second credential, works after Jellystat is gone.
- **No catalog, resolve names on demand**: rejected; counts, recently added,
  genres, and stale-media reports need the whole library, not one item.
