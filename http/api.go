package http

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
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
	r.With(hash, path).Head("/content/{"+paramHash+"}/*", h.head)
}

// writeAcquireError 把注册表返回的错误映射成 HTTP 状态码。
// /content 与 /list 共用同一套语义：超时 504，其余 404，客户端取消时不写响应。
func writeAcquireError(w http.ResponseWriter, hash metainfo.Hash, err error) {
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
}

func (h *handle) hash(w http.ResponseWriter, r *http.Request) {
	var hash = metainfo.NewHashFromHex(r.Context().Value(paramHash).(string))
	var whitelist = r.Context().Value(paramWhitelist).(map[string]struct{})
	var ignoretags = r.Context().Value(paramIgnoretags).(map[string]struct{})

	t, release, err := h.app.Acquire(r.Context(), hash)
	if err != nil {
		writeAcquireError(w, hash, err)
		return
	}
	defer release()

	render.Render(w, r, &playlist.PlayList{Torr: t, Whitelist: whitelist, IgnoreTags: ignoretags})
}

func (h *handle) magnet(w http.ResponseWriter, r *http.Request) {
	var magnet = r.Context().Value(paramMagnet).(*metainfo.Magnet)
	var whitelist = r.Context().Value(paramWhitelist).(map[string]struct{})
	var ignoretags = r.Context().Value(paramIgnoretags).(map[string]struct{})

	t, release, err := h.app.AcquireMagnet(r.Context(), magnet)
	if err != nil {
		writeAcquireError(w, magnet.InfoHash, err)
		return
	}
	defer release()

	render.Render(w, r, &playlist.PlayList{Torr: t, Whitelist: whitelist, IgnoreTags: ignoretags})
}

// head 只从缓存（内存 / bolt）回答元数据探测：不创建种子、不连 peer、不下载。
// 缓存未命中立即返回 503，由客户端决定重试或改用 GET。
func (h *handle) head(w http.ResponseWriter, r *http.Request) {
	var hash = metainfo.NewHashFromHex(r.Context().Value(paramHash).(string))
	var path = r.Context().Value(paramPath).(string)

	info, source, ok := h.app.CachedInfo(hash)
	if !ok {
		w.Header().Set("X-Cache", source)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	var name = filepath.Base(path)

	length, ok := infoFileLength(info, path)
	if !ok {
		w.Header().Set("X-Cache", source)
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	var hdr = w.Header()
	hdr.Set("X-Cache", source)
	hdr.Set("Accept-Ranges", "bytes")
	hdr.Set("Content-Disposition", `filename="`+url.PathEscape(name)+`"`)

	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		hdr.Set("Content-Type", ct)
	}

	if start, end, ok := parseSingleRange(r.Header.Get("Range"), length); ok {
		hdr.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, length))
		hdr.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		return
	}

	hdr.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusOK)
}

func (h *handle) content(w http.ResponseWriter, r *http.Request) {
	var hash = metainfo.NewHashFromHex(r.Context().Value(paramHash).(string))
	var path = r.Context().Value(paramPath).(string)

	// 引用计数 + 两段式回收的唯一入口。
	t, release, err := h.app.Acquire(r.Context(), hash)
	if err != nil {
		writeAcquireError(w, hash, err)
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
