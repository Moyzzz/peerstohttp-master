package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"sync"

	"github.com/go-chi/chi"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	application "github.com/WinPooh32/peerstohttp/app"
	peershttp "github.com/WinPooh32/peerstohttp/http"
	"github.com/WinPooh32/peerstohttp/settings"

	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/cors"
)

func newCors(origins []string) *cors.Cors {
	return cors.New(cors.Options{
		AllowedOrigins: origins,
		// HEAD 必须显式列出：chi 的 cors 中间件会按方法白名单过滤，
		// 不在名单里的方法直接不加任何 CORS 头，浏览器就会报跨域错误。
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodHead,
			http.MethodPost,
			http.MethodPut,
			http.MethodDelete,
			http.MethodPatch,
			http.MethodOptions,
		},
		// Range 不在 CORS 安全列表里，前端用 fetch 带 Range 时会触发预检，
		// 预检要求这里能匹配上 Access-Control-Request-Headers。
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "Range"},
		// 让跨域脚本能读到分段与缓存相关的响应头。
		ExposedHeaders:   []string{"Link", "Content-Range", "Accept-Ranges", "X-Cache"},
		AllowCredentials: true,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	})
}

func registerProfiler(r chi.Router) {
	r.HandleFunc("/debug/pprof/", pprof.Index)
	r.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	r.HandleFunc("/debug/pprof/profile", pprof.Profile)
	r.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	r.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// Manually add support for paths linked to by index page at /debug/pprof/
	r.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	r.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	r.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	r.Handle("/debug/pprof/block", pprof.Handler("block"))
	r.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	r.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
}

func init() {
	if !*settings.Service.JsonLogs {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})
	}
}

func main() {
	var err error

	var app *application.App

	var server http.Server
	var router chi.Router

	var wg sync.WaitGroup

	var ctx, cancel = context.WithCancel(context.Background())

	app, err = application.New(settings.Service)
	if err != nil {
		log.Fatal().Err(err).Msg("new app")
	}
	defer func() {
		err = app.Close()
		if err != nil {
			log.Fatal().Err(err).Msg("app clean up")
		}
	}()

	// Init router
	router = chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(newCors([]string{"*"}).Handler)

	peershttp.RouteApp(router, app)

	// Enable service profiling
	if *settings.Service.Profile {
		registerProfiler(router)
	}

	// Init HTTP server
	server.Addr = fmt.Sprintf("%s:%d", *settings.Service.Host, *settings.Service.Port)
	server.Handler = router

	// Serve
	handleStopSignals(ctx, cancel, &wg, &server, app.Client())
	serveHTTP(cancel, &wg, &server)

	wg.Wait()
}
