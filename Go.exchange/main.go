package main

import (
	"Go.exchange/core"
	"Go.exchange/initialize"
	"Go.exchange/tasks"
	"context"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"sync"

	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = ioutil.Discard
	initialize.InitAll() //读取配置
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	go func() {
		// 监听宿主机

		if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
			log.Println("Pprof error:", err)
		}
	}()
	tasks.StartAll(ctx, &wg)                    //启动后台任务
	srv := core.StartHttpServer()               //  启动 HTTP 服务
	core.WaitForShutdown(ctx, cancel, srv, &wg) //等待关闭信号 实现优雅退出

}
