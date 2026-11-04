package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
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

// infoFileLength 仅凭 metainfo 定位目标文件的长度，不需要 *torrent.Torrent。
// 路径规则与 findFile 保持一致：单文件种子按 basename 匹配，
// 多文件种子按 "{种子名}/{分片内路径}" 匹配。
func infoFileLength(info *metainfo.Info, path string) (int64, bool) {
	if info == nil {
		return 0, false
	}

	var clean = strings.Trim(filepath.ToSlash(path), "/")

	if !info.IsDir() {
		if filepath.Base(clean) != info.Name {
			return 0, false
		}

		return info.Length, true
	}

	for _, fi := range info.UpvertedFiles() {
		var p = strings.Join(append([]string{info.Name}, fi.Path...), "/")
		if p == clean {
			return fi.Length, true
		}
	}

	return 0, false
}

// parseSingleRange 解析单区间 Range 头（bytes=a-b / bytes=a- / bytes=-n）。
// 多区间、语法错误或超出范围时返回 false，由调用方回退成 200。
func parseSingleRange(header string, size int64) (start, end int64, ok bool) {
	const prefix = "bytes="

	if size <= 0 || !strings.HasPrefix(header, prefix) {
		return 0, 0, false
	}

	var spec = strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, false
	}

	var parts = strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}

	// 后缀区间：bytes=-500 表示最后 500 字节。
	if parts[0] == "" {
		var n, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}

		return size - n, size - 1, true
	}

	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}

	end = size - 1

	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false
		}
		if end >= size {
			end = size - 1
		}
	}

	return start, end, true
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
