# Recommendation Ranker rules_v3

## Signals and defaults

The only serving ranker is rules_v3. A user's profile is built from one canonical outcome per article. The canonical precedence is:

1. not_interested, unless a current ArticleReaction has a strictly later StateChangedAt;
2. the current liked reaction;
3. the latest passive signal, preferring a qualified/neutral/quick-bounce read outcome, then click, then view.

A tie between not-interested and the reaction keeps not-interested. An unlike does not create a like behavior projection; its event time is retained in ArticleReaction.StateChangedAt. Neutral reads and views can establish interaction/exclusion without adding affinity. Repeated views and feedback counts do not multiply a signal.

The default magnitudes are configured in config/config.yml. Contributions apply one time per canonical article outcome and decay by the configured half-life. Category and distinct normalized tag sums are saturated with tanh; missing or invalid metadata is not silently converted into a partial profile.

## Feedback and candidate serving

Feedback is queried as the latest row per (article, feedback type), then capped at 500 distinct articles. The latest per-type rows for those selected articles are used for canonicalization. View behavior is limited to the latest 200 distinct articles by LastSeenAt DESC, ID DESC.

not_interested suppression is an independent full-lookback NOT EXISTS candidate predicate. It remains effective outside the profile cap, unless a strictly later ArticleReaction.StateChangedAt supersedes it. Candidates are selected deterministically before the result cap; final scoring uses deterministic score, creation time, and article ID tie-breakers.

Zero metadata-backed personalized signals selects cold_start_rules_v3 and no_user_behavior. Otherwise serving records personalized_rules_v3. Tracking uses token version v2, and the configuration hash includes every signal, decay/lookback/saturation, scoring weight, cap, and read-policy input.

## Persistence and indexes

Recommendation facts retain raw read measurements plus the server-derived read_outcome; old max_scroll_depth, client qualified, and client quick-bounce columns are removed. ArticleReaction.StateChangedAt stores the behavior event OccurredAt, so reaction ordering is based on behavior time rather than projection time.

The V3 migration provides the feedback ordering and full-lookback negative-suppression indexes, plus the reaction state and latest-view indexes. Rebuilds use the same server-derived metrics as the live consumer.