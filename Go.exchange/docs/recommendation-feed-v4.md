# Recommendation Feed V4

This document records the implemented X-like `For You` pipeline. The serving
contract is frozen at `rules_v3`, `for_you_materialized_profile_v4`,
`social_semantic_materialized_profile_v4`, `materialized_profile_v1`,
`multi_signal_capped_v2`,
`read_end_recency_v2`, and `read_v1`. The tracking token protocol remains v2.

## Pipeline

1. Source projections invalidate `user_reco_profile_dirty` in the same
   transaction as view/like/feedback/reply changes. Article embedding changes
   use authoritative behavior and reaction fan-out.
2. A nearline materializer reconstructs bounded source history, canonicalizes
   multi-signal outcomes, writes `user_article_reco_states`, and replaces the
   profile vectors/evidence and raw author affinity atomically.
3. HTTP serving reads one profile row by user primary key. Compatible stale
   profiles remain usable while a durable recovery enqueue is retained; misses
   and incompatible profiles use a cold-start profile.
4. Recall Recent Semantic and Evergreen Semantic candidates with reserved
   capacity, alongside Following, Recent, and Trending sources. All source
   queries share public-scope, author, interaction, negative-interest, and
   served-history eligibility.
5. Hydrate authors and embeddings in batches, then rank by positive semantic
   similarity, confidence-weighted negative similarity, interaction affinity,
   follow bonus, freshness, time-decayed Trending, and deterministic article
   tie-breakers.
6. Select fresh candidates first, using network/novel-author balance, author
   sliding-window diversity, and candidate-level embedding diversity. Fill
   remaining positions from the soft-served pool without duplicates.
7. Persist request metadata and per-position result traces in one bounded
   transaction. A periodic cleanup task removes expired traces and requests in
   bounded batches; cleanup failures remain non-fatal and observable.

## Materialized profile

The profile identity is `materialized_profile_v1`; canonical outcomes remain
`multi_signal_capped_v2`. The materializer stores nullable positive and
negative pgvector columns, signal counts, negative evidence, `ComputedAt`, and
`NextRebuildAt`. A valid cold-start profile may contain NULL/NULL vectors with
`Dimensions = 0`.

`UserRecoProfileDirty` is a per-user versioned queue. True source invalidation
increments `DirtyVersion` and resets retry metadata. Serving recovery and
periodic rebase use an insert-if-absent enqueue and never reset an existing
retry. Each batch uses a deterministic PostgreSQL transaction advisory lock;
lock contention is skipped, and one-user failures retry with capped exponential
backoff without stopping the batch.

The canonical state table contains every interacted article, including a
reaction-only unliked row. Candidate exclusion uses that state table only for
compatible hit/stale profiles; miss and incompatible profiles retain the
explicit current not-interested/later-like/later-reply eligibility checks.
Affinity and following lookups are scoped to hydrated candidate authors, with
raw affinity saturated at rank time.

## Semantic recall

The default Semantic cap is divided into two nearest-neighbor pools:

```text
Recent window: 30 days
Recent ratio:  0.80
```

For a cap of one, the Recent quota is one. For larger caps, the Recent quota is
`round(cap * ratio)` clamped to `[1, cap-1]`; Evergreen receives the remainder.
Recent uses `published_at >= cutoff`, Evergreen uses `published_at < cutoff`,
and both order by embedding distance followed by descending article ID.

The normal path uses two Semantic queries. If either reserved pool underfills,
one all-age nearest-neighbor backfill query excludes every selected article ID.
The result never exceeds the Semantic cap and never duplicates an article.

## Trending recall and ranking

Trending uses existing article counters and is intentionally not an event-rate
or velocity model. Defaults are:

```text
maximum age:    7 days
half-life:      24 hours
comment factor: 0.5
weight:         0.5
```

The recall query requires a public article published within the maximum age and
positive engagement (`like_count > 0 OR comment_count > 0`). Its ordering is:

```text
(
  ln(1 + max(likes, 0))
  + comment_factor * ln(1 + max(comments, 0))
)
* exp(-ln(2) * max(age_hours, 0) / half_life_hours)
DESC, published_at DESC, id DESC
```

The ranker recalculates the same raw feature for every hydrated candidate,
regardless of how that candidate entered recall. Future publication times have
zero age; articles beyond the maximum age or with zero engagement receive a
zero Trending feature.

## Operational contract

The Following feed endpoint remains unchanged. For You exposes source and
selection metadata through the existing recommendation response and keeps the
tracking token at v2. Request/trace persistence and cleanup are best-effort for
availability: feed serving continues when trace persistence or served-history
loading fails, while bounded failure counters are emitted.

The PostgreSQL migration creates retrieval indexes, trace keys and indexes,
profile/state/dirty tables, vector dimension checks, request profile telemetry
checks, and explicit cascade foreign keys. It also removes the legacy source
columns and retrieval index idempotently. The acceptance path uses a disposable
PostgreSQL 16 database with pgvector; Redis/Kafka/embedding runtime health is
not substituted with fake DSNs or SQLite.
