package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"

	"gorm.io/gorm"
)

const dateLayout = "2006-01-02"

func main() {
	fromValue := flag.String("from", "", "first UTC date to rebuild (YYYY-MM-DD)")
	toValue := flag.String("to", "", "last UTC date to rebuild (YYYY-MM-DD)")
	flag.Parse()

	from, to, err := parseDateRange(*fromValue, *toValue)
	if err != nil {
		log.Fatal(err)
	}
	config.InitDatabaseConfig()
	if err := rebuildRecommendationMetrics(from, to); err != nil {
		log.Fatalf("rebuild recommendation metrics: %v", err)
	}
	log.Printf("rebuilt recommendation metrics from %s through %s UTC", from.Format(dateLayout), to.Format(dateLayout))
}

func parseDateRange(fromValue, toValue string) (time.Time, time.Time, error) {
	if fromValue == "" || toValue == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("both --from and --to are required")
	}
	from, err := time.ParseInLocation(dateLayout, fromValue, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --from date: %w", err)
	}
	to, err := time.ParseInLocation(dateLayout, toValue, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --to date: %w", err)
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("--from must not be after --to")
	}
	return from, to, nil
}

func rebuildRecommendationMetrics(from, to time.Time) error {
	if global.Db == nil {
		return fmt.Errorf("database is not initialized")
	}
	exclusiveEnd := to.AddDate(0, 0, 1)
	return global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"DELETE FROM recommendation_daily_metrics WHERE metric_date >= ? AND metric_date <= ?",
			from, to,
		).Error; err != nil {
			return err
		}
		return tx.Exec(`
INSERT INTO recommendation_daily_metrics (
  metric_date, scene, ranker_version, ranker_config_hash, strategy_id,
  position, article_id, impression_count, click_count, qualified_read_count,
  quick_bounce_count, not_interested_count, feed_dwell_count, feed_visible_time_ms, updated_at
)
SELECT
  (occurred_at AT TIME ZONE 'UTC')::date AS metric_date,
  scene,
  ranker_version,
  ranker_config_hash,
  strategy_id,
  position,
  article_id,
  SUM(CASE WHEN event_type = 'impression' THEN 1 ELSE 0 END) AS impression_count,
  SUM(CASE WHEN event_type = 'click' THEN 1 ELSE 0 END) AS click_count,
  SUM(CASE WHEN event_type = 'read_end' AND read_outcome = 'qualified' THEN 1 ELSE 0 END) AS qualified_read_count,
  SUM(CASE WHEN event_type = 'read_end' AND read_outcome = 'quick_bounce' THEN 1 ELSE 0 END) AS quick_bounce_count,
  SUM(CASE WHEN event_type = 'not_interested' THEN 1 ELSE 0 END) AS not_interested_count,
  SUM(CASE WHEN event_type = 'feed_dwell' THEN 1 ELSE 0 END) AS feed_dwell_count,
  SUM(CASE WHEN event_type = 'feed_dwell' THEN COALESCE(feed_visible_time_ms, 0) ELSE 0 END) AS feed_visible_time_ms,
  NOW() AS updated_at
FROM recommendation_events
WHERE occurred_at >= ? AND occurred_at < ?
GROUP BY
  (occurred_at AT TIME ZONE 'UTC')::date,
  scene, ranker_version, ranker_config_hash, strategy_id, position, article_id
`, from, exclusiveEnd).Error
	})
}
