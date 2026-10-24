package main

import (
	"net/http"
	"time"
)

// dateRangeDays maps the API's dateRange values to a lookback window.
var dateRangeDays = map[string]int{"7days": 7, "30days": 30, "90days": 90}

func nextDay(date string) string { return daysAfter(date, 1) }

func daysAfter(date string, n int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

type stageMetric struct {
	Count int     `json:"count"`
	Value float64 `json:"value"`
}

func (a *app) dashboardMetrics(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	rangeKey := r.URL.Query().Get("dateRange")
	if rangeKey == "" {
		rangeKey = "30days"
	}
	days, ok := dateRangeDays[rangeKey]
	if !ok {
		return errBadRequest("dateRange must be 7days, 30days or 90days")
	}
	since := daysAfter(todayStamp(), -days)
	monthStart := todayStamp()[:8] + "01"

	var out struct {
		Summary struct {
			TotalContacts      int     `json:"totalContacts"`
			ActiveDeals        int     `json:"activeDeals"`
			TotalPipelineValue float64 `json:"totalPipelineValue"`
			ClosedThisMonth    int     `json:"closedThisMonth"`
			WinRate            float64 `json:"winRate"`
		} `json:"summary"`
		ByStage    map[string]stageMetric `json:"byStage"`
		TopDeals   []dealOut              `json:"topDeals"`
		RecentActs []activityOut          `json:"recentActivities"`
	}
	out.ByStage = map[string]stageMetric{}

	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM contacts WHERE deleted_at IS NULL`).Scan(&out.Summary.TotalContacts); err != nil {
		return err
	}
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(value), 0) FROM deals
		 WHERE deleted_at IS NULL AND stage NOT IN ('closed_won', 'closed_lost')`).
		Scan(&out.Summary.ActiveDeals, &out.Summary.TotalPipelineValue); err != nil {
		return err
	}
	if err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deals WHERE deleted_at IS NULL AND stage = 'closed_won'
		 AND closed_date >= ?`, monthStart).Scan(&out.Summary.ClosedThisMonth); err != nil {
		return err
	}

	var won, lost int
	if err := a.db.QueryRowContext(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN stage = 'closed_won' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN stage = 'closed_lost' THEN 1 ELSE 0 END), 0)
		 FROM deals WHERE deleted_at IS NULL AND closed_date >= ?`, since).Scan(&won, &lost); err != nil {
		return err
	}
	if won+lost > 0 {
		out.Summary.WinRate = float64(won) / float64(won+lost) * 100
	}

	rows, err := a.db.QueryContext(ctx,
		`SELECT stage, COUNT(*), COALESCE(SUM(value), 0) FROM deals
		 WHERE deleted_at IS NULL AND created_at >= ? GROUP BY stage`, since)
	if err != nil {
		return err
	}
	for rows.Next() {
		var stage string
		var m stageMetric
		if err := rows.Scan(&stage, &m.Count, &m.Value); err != nil {
			rows.Close()
			return err
		}
		out.ByStage[stage] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	top, _, err := queryPage(ctx, a.db, dealCols, dealFrom,
		" WHERE d.deleted_at IS NULL AND d.stage NOT IN ('closed_won', 'closed_lost')",
		" ORDER BY d.value DESC, d.id ASC", nil, pageParams{1, 5}, scanDeal)
	if err != nil {
		return err
	}
	out.TopDeals = top

	acts, _, err := a.listActivities(ctx, " WHERE 1=1", nil, " ORDER BY a.created_at DESC, a.rowid DESC", pageParams{1, 10})
	if err != nil {
		return err
	}
	out.RecentActs = acts

	writeJSON(w, http.StatusOK, out)
	return nil
}
