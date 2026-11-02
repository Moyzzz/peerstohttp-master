package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/rs/zerolog/log"
)

var errFileNotFound = errors.New("file not found")

func serveTorrentDir(w http.ResponseWriter, r *http.Request, t *torrent.Torrent, path string) error {
	var err error
	//var name string
	//
	//if path == "" {
	//	name = t.Name()
	//} else {
	//	name = filepath.Base(path)
	//}
	//
	//w.Header().Set("Content-Disposition", `filename="`+url.PathEscape(name+".zip")+`"`)
	//w.Header().Set("Content-Type", "application/zip")
	//w.WriteHeader(http.StatusOK)
	//
	//var reader = t.NewReader()
	//defer reader.Close()
	//
	//// TODO адаптер из файлов торрента
	//
	//var zipWriter io.WriteCloser
	//zipWriter = zip.NewWriter(w)
	//
	//for _, f := range t.Files() {
	//
	//	header, err := zip.FileInfoHeader(f)
	//	if err != nil {
	//		return err
	//	}
	//
	//
	//}
	//
	//_, err = io.Copy(zipWriter, reader)
	return err
}

func serveTorrentFile(w http.ResponseWriter, r *http.Request, t *torrent.Torrent, path string) error {
	var name string

	var file *torrent.File
	var ok bool

	log.Info().Msgf("file path = %s", path)

	file, ok = findFile(t, path)

	if !ok || file == nil {
		return errFileNotFound
	}

	var reader = file.NewReader()
	defer reader.Close()

	fip := file.FileInfo().Path
	if len(fip) == 0 {
		name = file.DisplayPath()
	} else if len(fip) == 1 {
		name = fip[0]
	} else {
		name = fip[len(fip)-1]
	}

	return serveContent(w, r, file.Length(), reader, name)
}

func findFile(t *torrent.Torrent, path string) (*torrent.File, bool) {
	var file *torrent.File

	if !t.Info().IsDir() {
		if filepath.Base(path) != t.Info().Name {
			return nil, false
		}
		file = t.Files()[0]
	} else {
		for _, f := range t.Files() {
			var p = f.Path()
			if p == path {
				file = f
				break
			}
		}
		if file == nil {
			return nil, false
		}
	}

	return file, true
}

func serveContent(w http.ResponseWriter, r *http.Request, size int64, reader torrent.Reader, name string) error {
	var err error

	// Don't wait for pieces to complete and be verified.
	reader.SetResponsive()

	if size > 0 {
		// Read ahead 10% of file.
		reader.SetReadahead((size * 10) / 100)
	}

	w.Header().Set("Content-Disposition", `filename="`+url.PathEscape(name)+`"`)

	_, err = reader.Seek(0, 0)
	if err != nil {
		return err
	}

	http.ServeContent(w, r, name, time.Now(), ctxReadSeeker{Reader: reader, ctx: r.Context()})
	return nil
}

// ctxReadSeeker 让底层 torrent.Reader 的读取跟随请求上下文。
//
// torrent.Reader.Read 内部固定使用 context.Background()，而 http.ServeContent
// 只会调用 io.Reader.Read。客户端断开后，阻塞在"等分片"上的读取无法被感知，
// handler 不返回、引用计数无法归零，种子因此永远不会被两段式回收。
// 包装一层后，读取改为走 ReadContext(ctx, b)，客户端一断开就立即返回错误。
type ctxReadSeeker struct {
	torrent.Reader
	ctx context.Context
}

func (c ctxReadSeeker) Read(b []byte) (int, error) {
	return c.Reader.ReadContext(c.ctx, b)
}
