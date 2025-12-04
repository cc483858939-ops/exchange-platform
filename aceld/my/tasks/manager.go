package tasks

import (
	"context"
	"sync"
)

func StartAll(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go SyncLikesToMySQL(ctx, wg) //后续如果有其他要添加的依然可以写在后面
}
