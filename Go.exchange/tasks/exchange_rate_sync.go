package tasks

import (
	"context"
	"log"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/services"
)

func startExchangeRateRefresh(ctx context.Context, wg *sync.WaitGroup) {
	interval := config.ExchangeRateRefreshInterval()
	wg.Add(1)
	go func() {
		defer wg.Done()
		refresh := func() {
			requestCtx, cancel := context.WithTimeout(ctx, config.ExchangeRateRequestTimeout())
			defer cancel()
			if _, err := services.DefaultExchangeRateService().Refresh(requestCtx); err != nil && ctx.Err() == nil {
				log.Printf("[ExchangeRate] refresh failed: %v", err)
			}
		}

		refresh()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("[ExchangeRate] refresh task stopped")
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
}
