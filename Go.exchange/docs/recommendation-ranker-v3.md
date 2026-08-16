# Recommendation Ranker rules_v3

## Signals and defaults

The only serving ranker is rules_v3. A user's profile is built from one canonical outcome per article. The canonical precedence is:

1. not_interested, unless a current ArticleReaction has a strictly later StateChangedAt;
2. the current liked reaction;
3. the passive outcome resolved by the read_end_recency_v2 policy:
   - a read_end is terminal only until a strictly later recommendation click or ArticleBehavior view;
   - a strictly later click supersedes the read_end;
   - otherwise, a strictly later view supersedes the read_end;
   - if both click and view are strictly later, click remains canonical;
   - without a read_end, click remains preferred over view;
   - equal timestamps do not supersede the read_end.

A tie between not-interested and the reaction keeps not-interested. An unlike does not create a like behavior projection; its event time is retained in ArticleReaction.StateChangedAt. Neutral reads and views can establish interaction/exclusion without adding affinity. Repeated views and feedback counts do not multiply a signal. The canonical policy version is read_end_recency_v2.

The default magnitudes are configured in config/config.yml. Contributions apply one time per canonical article outcome and decay by the configured half-life. Category and distinct normalized tag sums are saturated with tanh; missing or invalid metadata is not silently converted into a partial profile.

## Feedback and candidate serving

Feedback is queried as the latest row per (article, feedback type), then capped at 500 distinct articles. The latest per-type rows for those selected articles are used for canonicalization. View behavior is limited to the latest 200 distinct articles by LastSeenAt DESC, ID DESC.

Candidate retrieval is versioned as multi_source_v1 and runs in two stages. Stage A returns IDs only, using the current public-article scope, interacted-article exclusion, and the full-lookback not_interested suppression predicate with a later ArticleReaction.StateChangedAt override:

1. personalized_category: when PersonalizedSignalCount is positive, the top 8 positive normalized categories are recalled with one window-function query. The source contributes up to 200 completed IDs, interleaved deterministically by per-category row number, affinity, normalized label, created_at DESC, and id DESC.
2. recent: completed public IDs ordered by created_at DESC, id DESC, capped at 150.
3. popular: completed public IDs ordered by like_count DESC, created_at DESC, id DESC, capped at 150.

The sources are merged in category, recent, popular order. First occurrence wins, duplicates are removed, and the completed ID pool is capped at 500. If that pool contains fewer than the requested result limit, fallback recalls only the missing count from public non-completed articles ordered by like_count DESC, created_at DESC, id DESC. Fallback does not perform personalized category recall. The merged IDs are hydrated once with public article columns and preloaded public authors; hydration explicitly restores ID merge order and rechecks public visibility so deleted, expired, future, or unpublished rows cannot leak.

Cold start keeps the existing cold_start_rules_v3 and no_user_behavior strategy IDs: category recall is skipped, while recent, popular, and the conditional fallback remain available. Final ranking and the API response schema are unchanged.

not_interested suppression is an independent full-lookback NOT EXISTS candidate predicate. It remains effective outside the profile cap, unless a strictly later ArticleReaction.StateChangedAt supersedes it. Candidates are selected deterministically before the result cap; final scoring uses deterministic score, creation time, and article ID tie-breakers.

Zero metadata-backed personalized signals selects cold_start_rules_v3 and no_user_behavior. Otherwise serving records personalized_rules_v3. Tracking uses token version v2. The configuration hash includes every signal, decay/lookback/saturation, scoring weight, retrieval version, top-category count, per-source cap, merged cap, feedback/view cap, and read-policy input.

## Persistence and indexes

Recommendation facts retain raw read measurements plus the server-derived read_outcome; old max_scroll_depth, client qualified, and client quick-bounce columns are removed. ArticleReaction.StateChangedAt stores the behavior event OccurredAt, so reaction ordering is based on behavior time rather than projection time.

The V3 migration provides the feedback ordering and full-lookback negative-suppression indexes, the reaction state and latest-view indexes, and the partial category-expression and popularity indexes used by multi_source_v1 retrieval. Rebuilds use the same server-derived metrics as the live consumer.