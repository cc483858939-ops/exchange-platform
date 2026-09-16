package dashboards

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type dashboardContract struct {
	Title   string `json:"title"`
	UID     string `json:"uid"`
	Refresh string `json:"refresh"`
	Time    struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"time"`
	Panels []struct {
		Datasource string `json:"datasource"`
		Title      string `json:"title"`
		Targets    []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
}

func TestLoadTestDashboardContract(t *testing.T) {
	raw, err := os.ReadFile("go-exchange-loadtest.json")
	if err != nil {
		t.Fatal(err)
	}

	var dashboard dashboardContract
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatalf("dashboard JSON is invalid: %v", err)
	}
	if dashboard.Title != "Go.exchange Load Test" {
		t.Fatalf("title=%q", dashboard.Title)
	}
	if dashboard.UID != "go-exchange-loadtest" {
		t.Fatalf("uid=%q", dashboard.UID)
	}
	if dashboard.Refresh != "15s" || dashboard.Time.From != "now-15m" || dashboard.Time.To != "now" {
		t.Fatalf("time configuration refresh=%q from=%q to=%q", dashboard.Refresh, dashboard.Time.From, dashboard.Time.To)
	}

	requiredTitles := map[string]bool{
		"Total HTTP QPS":                  false,
		"Overall HTTP P95":                false,
		"HTTP Error Rate":                 false,
		"QPS by Route":                    false,
		"P95 by Route":                    false,
		"5xx by Route":                    false,
		"Recommendation Generation P95":   false,
		"Recommendation Request Outcomes": false,
		"Worker Pipeline Backlog":         false,
		"Worker Pipeline Health":          false,
		"Notification Consumer Lag":       false,
	}
	requiredMetrics := map[string]bool{
		"go_exchange_http_requests_total":                               false,
		"go_exchange_http_request_duration_seconds_bucket":              false,
		"go_exchange_recommendation_generation_duration_seconds_bucket": false,
		"go_exchange_recommendation_requests_total":                     false,
		"worker_pipeline_backlog":                                       false,
		"worker_pipeline_healthy":                                       false,
		"go_exchange_notification_consumer_lag":                         false,
	}

	for _, panel := range dashboard.Panels {
		if _, required := requiredTitles[panel.Title]; required {
			requiredTitles[panel.Title] = true
		}
		if panel.Datasource != "Prometheus" {
			t.Fatalf("panel %q datasource=%q", panel.Title, panel.Datasource)
		}
		for _, target := range panel.Targets {
			for metric := range requiredMetrics {
				if strings.Contains(target.Expr, metric) {
					requiredMetrics[metric] = true
				}
			}
		}
	}
	for title, found := range requiredTitles {
		if !found {
			t.Errorf("required panel %q is missing", title)
		}
	}
	for metric, found := range requiredMetrics {
		if !found {
			t.Errorf("required metric %q is missing from targets", metric)
		}
	}
}
