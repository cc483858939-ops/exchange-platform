package main

import (
	"aceld/core"
	"aceld/initialize"
	"aceld/tasks"
	"context"
	"sync"
)

func main() {
	initialize.InitAll() //读取配置
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	tasks.StartAll(ctx, &wg)                    //启动后台任务
	srv := core.StartHttpServer()               //  启动 HTTP 服务
	core.WaitForShutdown(ctx, cancel, srv, &wg) //等待关闭信号 实现优雅退出
}
