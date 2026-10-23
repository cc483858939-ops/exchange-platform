# Recommendation candidate snapshot v5

Recommendation cursors use the `recommendation:pool:v5:{snapshotID}` Redis
Hash. The v5 namespace and cursor signature version replace the former
single-string JSON snapshot; there is no old-format read path.

| Hash field | Contents |
| --- | --- |
| `meta` | Owner, immutable serving/ranker identity, absolute expiry, page count, recall cursors, revision, and exhausted state |
| `pool` | Current candidate pool, capped at 500 candidates |
| `page:{index}` | One page's prepared or committed result, replay key, and side-effect receipts |
| `post:{id}` | Exact lifecycle state: `reserved:{page}`, `used:{page}`, `rejected:{page}`, or `invalid:{page}` |
| `context` | Recent selected candidates used for cross-page author and semantic ranking context |
| `pending_history` | Sorted page indexes whose committed Served History receipt still needs recovery |
| `_bytes` | Approximate Hash field/value byte count maintained inside each Lua mutation |

Normal paging reads `meta` and `pool`, the requested page, the bounded context,
and membership for the candidate IDs under consideration. It never reads all
page fields. Historical recall checks exact post membership for each bounded
repository batch rather than downloading the snapshot's full used-ID history.

Page preparation, ID reservation, pool/cursor updates, context updates, page
commit, mapper-rejection release, pending-history indexing, and revision advance
use one Lua compare-and-swap on the Hash. A prepared page reserves its IDs;
commit changes returned IDs to `used`, mapper-rejected IDs to `rejected`, and
releases other omitted IDs. Invalidating later uncommitted pages removes those
page fields and releases their reservations in the same mutation. Committed
later pages block an incompatible earlier-page change.

The Hash receives one absolute TTL at creation. Updates do not call `PEXPIRE`,
so paging cannot extend the cursor lifetime. A missing page below `page_count`
is reported as corrupt/unavailable state; a missing Hash is `CURSOR_EXPIRED`.

## Resource limits and recovery

- Candidate pool: 500 candidates / 1 MiB encoded.
- Page: at most 50 items / 64 KiB encoded.
- Snapshot: 128 pages, 7,000 Hash fields, and 8 MiB accounted field/value data.
- Context: the latest `max(AuthorWindowSize, SemanticHistoryWindowSize)` entries,
  capped at 512. The semantic-history window defaults to 128; current-page
  selection still compares candidates within the page.
- One atomic mutation changes at most 8 later page records and 1,024 post-state
  fields. Pending Served History recovery reads at most 8 pages per pass.

At the page or storage budget, the service returns the existing HTTP 410
`CURSOR_EXPIRED` flow and records the bounded `capacity_rotation` outcome. It
does not return `depleted: true`. The client can rebuild the snapshot while
retaining its displayed posts. The normal cursor TTL remains 120 seconds.

Served History recovery is indexed by `pending_history`, uses the stable
`snapshotID:pageIndex` history key, and removes the index entry only after the
page receipt is stored. Trace remains best effort: `TraceAttempted` is claimed
before enqueue and is not replayed after that claim.
