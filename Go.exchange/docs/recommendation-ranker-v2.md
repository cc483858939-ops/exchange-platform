# Recommendation Ranker rules_v2

## Signals and defaults

The only serving ranker is `rules_v2`. Active `ArticleBehavior` rows yield view (`+0.5`) and like (`+6`). Raw recommendation events yield click (`+1.5`), qualified read (`+3`), quick bounce (`-3`), and not interested (`-6`). Impressions and neutral reads are not affinity signals; clicks and every read end still exclude their article from the current user's future candidates.

All magnitudes are configurable. The defaults are a 14-day half-life, 90-day feedback lookback, 6-point interest saturation scale, global 500-event feedback limit, and latest-5 cap per `(article, normalized signal)`.

## Profile and score

For each signal, `effective_weight = signed_magnitude * count_factor * exp(-ln(2) * age_days / half_life_days)`. `ArticleBehavior.LastSeenAt` is explicitly an aggregate-time approximation. Category and distinct normalized tag raw sums are bounded with `tanh(raw / interest_saturation_scale)`, so affinity is always in `[-1, 1]`.

`score = category_interest * category_weight + average(distinct tag interests) * tag_weight + log(like_count + 1) * popularity_weight + freshness_score * freshness_weight + analysis_state_bonus`.

Candidates are selected deterministically before the cap: completed uses `created_at DESC, id DESC`; fallback uses `like_count DESC, created_at DESC, id DESC`. Final results use score descending, then creation time descending, then article ID descending.

## Feedback and serving

Feedback is user scoped and ordered by `occurred_at DESC, received_at DESC, event_id DESC`. `not_interested` uses an independent `NOT EXISTS` candidate suppression query for the full lookback, so it remains effective even when it falls outside the 500-event profile slice. The required partial indexes are `idx_recommendation_events_user_feedback_order` and `idx_recommendation_events_user_article_negative`.

`PersonalizedSignalCount` counts accepted, metadata-backed non-neutral behavior/feedback contributions. Zero selects `cold_start_rules_v2` and `no_user_behavior`; otherwise serving records `personalized_rules_v2`. Any behavior, feedback, metadata, or candidate load failure returns HTTP 500 rather than a partial profile. Tracking protocol and response schema are unchanged. The ranker configuration hash includes every signal magnitude, decay/lookback/saturation setting, scoring weight, and query cap.