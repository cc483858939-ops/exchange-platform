# Recommendation Feed V2

This document records the implemented X-like `For You` pipeline. The production
contract is frozen at `rules_v2`, `for_you_rules_v2`,
`social_semantic_multi_source_v2`, `multi_signal_capped_v2`,
`read_end_recency_v2`, and `read_v1`.

## Pipeline

1. Load public, active article behavior and feedback signals. Likes and replies
   are independent positive facts; quick-bounce and negative-interest signals
   build a separate negative vector. A later click/view/reply supersedes a
   passive `read_end`; an equal timestamp retains `read_end`.
2. Build capped positive and negative interest vectors from active embeddings,
   preserving interaction IDs even when an embedding is unavailable.
3. Recall semantic, following, recent, and popular candidates with bounded
   source caps. Eligibility applies self-author, public-scope, full-history
   negative-interest, interacted, and served-history rules in SQL.
4. Hydrate authors and embeddings in batches, then rank by positive semantic
   similarity, confidence-weighted negative similarity, interaction affinity,
   follow bonus, freshness, popularity, and deterministic ID tie-breakers.
5. Select fresh candidates first, using network/novel-author balance, author
   sliding-window diversity, and candidate-level embedding diversity. Fill
   remaining positions from the soft-served pool without duplicates.
6. Persist request metadata and per-position result traces in one bounded
   transaction. A periodic cleanup task removes expired traces and requests in
   bounded batches; cleanup failures remain non-fatal and observable.

## Operational contract

The Following feed endpoint remains unchanged. For You exposes source and
selection metadata through the existing recommendation response and keeps the
tracking token at v2. Request/trace persistence and cleanup are best-effort for
availability: feed serving continues when trace persistence or served-history
loading fails, while bounded failure counters are emitted.

The PostgreSQL migration creates retrieval indexes, trace keys and indexes,
request checks, and explicit cascade foreign keys. The acceptance path uses a
disposable PostgreSQL 16 database; Redis/Kafka/embedding runtime health is not
substituted with fake DSNs or SQLite.
