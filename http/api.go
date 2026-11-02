package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/go-chi/chi"
	"github.com/go-chi/render"
	"github.com/rs/zerolog/log"

	"github.com/WinPooh32/peerstohttp/app"
	"github.com/WinPooh32/peerstohttp/http/host"
	list_render "github.com/WinPooh32/peerstohttp/http/render"
	"github.com/WinPooh32/peerstohttp/playlist"
)

const (
	paramMagnet     = "magnet"
	paramHash       = "hash"
	paramPath       = "path"
	paramWhitelist  = "whitelist"
	paramIgnoretags = "ignoretags"
)

var (
	patternList = fmt.Sprintf("%s:[json,m3u,html]+", list_render.ParamContentType)
)

type handle struct {
	app *app.App
}

func RouteApp(r chi.Router, app *app.App) {
	var h = handle{
		app: app,
	}

	r.Route("/list/{"+patternList+"}/{"+paramWhitelist+"}/{"+paramIgnoretags+"}/", func(r chi.Router) {
		r.Use(
			whitelist,
			ignoretags,
			host.Host,
			list_render.ListContentType,
			list_render.SetListResponder,
		)

		r.With(hash).Get("/hash/{hash}", h.hash)
		r.With(magnet).Get("/magnet/*", h.magnet)
	})

	r.With(hash, path).Get("/content/{"+paramHash+"}/*", h.content)
}

func (h *handle) hash(w http.ResponseWriter, r *http.Request) {
	var hash = r.Context().Value(paramHash).(string)
	var whitelist = r.Context().Value(paramWhitelist).(map[string]struct{})
	var ignoretags = r.Context().Value(paramIgnoretags).(map[string]struct{})

	var t, err = h.app.TrackHashContext(r.Context(), metainfo.NewHashFromHex(hash))
	if err != nil {
		log.Error().Err(err).Msg("track by hash")
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	render.Render(w, r, &playlist.PlayList{Torr: t, Whitelist: whitelist, IgnoreTags: ignoretags})
}

func (h *handle) magnet(w http.ResponseWriter, r *http.Request) {
	var magnet = r.Context().Value(paramMagnet).(*metainfo.Magnet)
	var whitelist = r.Context().Value(paramWhitelist).(map[string]struct{})
	var ignoretags = r.Context().Value(paramIgnoretags).(map[string]struct{})

	var t, err = h.app.TrackMagnetContext(r.Context(), magnet)
	if err != nil {
		log.Error().Err(err).Msg("track by hash")
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	render.Render(w, r, &playlist.PlayList{Torr: t, Whitelist: whitelist, IgnoreTags: ignoretags})
}

func (h *handle) content(w http.ResponseWriter, r *http.Request) {
	var hash = metainfo.NewHashFromHex(r.Context().Value(paramHash).(string))
	var path = r.Context().Value(paramPath).(string)

	// 引用计数 + 两段式回收的唯一入口。
	t, release, err := h.app.Acquire(r.Context(), hash)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrInfoTimeout):
			log.Warn().Err(err).Str("hash", hash.HexString()).Msg("acquire torrent")
			http.Error(w, http.StatusText(http.StatusGatewayTimeout), http.StatusGatewayTimeout)

		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// 客户端已经离开，无需再写响应。
			log.Debug().Err(err).Str("hash", hash.HexString()).Msg("acquire torrent")

		default:
			log.Warn().Err(err).Str("hash", hash.HexString()).Msg("acquire torrent")
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		}
		return
	}
	defer release()

	var serveErr error

	if t.Info().IsDir() && strings.Count(path, "/") == 0 {
		serveErr = serveTorrentDir(w, r, t, path)
	} else {
		serveErr = serveTorrentFile(w, r, t, path)
	}

	if serveErr != nil {
		// ServeContent 可能已经写出响应，这里只记录，避免多余的 WriteHeader。
		log.Warn().Err(serveErr).Msg("serve content")
	}
}

//func fileInfoHeader(fi *torrent.File) (*zip.FileHeader, error) {

//}
