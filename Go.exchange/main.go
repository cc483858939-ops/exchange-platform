package main

import (
	"Go.exchange/config"
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
	initialize.InitAll()

	role := config.RuntimeRole()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	go func() {
		if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
			log.Println("Pprof error:", err)
		}
	}()
	if role != config.RuntimeRoleAPI {
		tasks.StartAll(ctx, &wg)
	}
	log.Printf("starting go.exchange in %s mode", role)

	srv := core.StartHttpServer()
	core.WaitForShutdown(ctx, cancel, srv, &wg)
}
