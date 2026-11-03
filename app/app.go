package app

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/rs/zerolog/log"
	"go.etcd.io/bbolt"

	"github.com/WinPooh32/peerstohttp/settings"

	"github.com/anacrolix/torrent/metainfo"
)

const (
	dbName       = ".app.bolt.db"
	dbBucketInfo = "torrent_info"
)

var trackers = [][]string{
	{"udp://opentor.org:2710", "https://bt.t-ru.org/ann?magnet", "http://bt.t-ru.org/ann?magnet"},
	{"udp://tracker.coppersurfer.tk:6969/announce"},
	{"http://retracker.local/announce"},
}

// TODO add torrent management(disk cache size control, start/stop and etc.).
type App struct {
	client *torrent.Client

	// active 是种子注册表（/content 与 /list 共用），负责引用计数与两段式回收。
	active map[metainfo.Hash]*trackedTorrent
	mu     sync.RWMutex

	// idleTTL 是 refs 归零后到真正 Drop 之间的宽限期。
	idleTTL time.Duration
	// disconnectTTL 是 refs 归零后到"断开全部 peer 连接"之间的宽限期。
	// 设为 0 表示立即断连（旧行为）。
	disconnectTTL time.Duration
	// infoTimeout 是等待种子元数据的上限。
	infoTimeout time.Duration
	// maxConns 是种子的连接上限，用于阶段一之后的恢复。
	maxConns int
	// keepMeta 决定 Drop 之后是否保留数据库中的 metainfo。
	keepMeta bool

	db *bbolt.DB

	// Path to temporary data folder.
	tmp string
	cwd string
}

func New(service *settings.Settings) (*App, error) {
	var err error
	var tmp string
	var cwd string

	var client *torrent.Client
	var store *bbolt.DB

	// Working directory.
	if *service.DownloadDir == "" {
		tmp, err = ioutil.TempDir("", "peerstohttp")
		if err != nil {
			return nil, fmt.Errorf("create temp. folder: %w", err)
		}
		cwd = tmp
	} else {
		cwd = *service.DownloadDir
	}

	err = os.MkdirAll(cwd, os.ModePerm)
	if err != nil {
		return nil, fmt.Errorf("failed to create dir: %w", err)
	}

	client, err = p2p(service, cwd)
	if err != nil {
		return nil, fmt.Errorf("new torrent client: %w", err)
	}

	store, err = db(fmt.Sprintf("%s/%s", cwd, dbName))
	if err != nil {
		return nil, fmt.Errorf("new db: %w", err)
	}

	var app = &App{
		active:        map[metainfo.Hash]*trackedTorrent{},
		client:        client,
		db:            store,
		tmp:           tmp,
		cwd:           cwd,
		idleTTL:       time.Duration(*service.IdleTTL) * time.Second,
		disconnectTTL: time.Duration(*service.DisconnectTTL) * time.Second,
		infoTimeout:   time.Duration(*service.InfoTimeout) * time.Second,
		maxConns:      *service.MaxConnections,
		keepMeta:      *service.KeepMeta,
	}

	// 断连宽限不应长于空闲淘汰，否则阶段一永远不会发生。
	if app.disconnectTTL > app.idleTTL {
		app.disconnectTTL = app.idleTTL
	}

	go func() {
		err = app.load()
		if err != nil {
			log.Error().
				Err(err).
				Msg("load app state from db")
			return
		}
	}()

	log.Info().Msg("app loaded")

	return app, nil
}

// load 只做一次元数据缓存体检，不再把历史种子全部 AddTorrent。
// 种子改为按需创建：/content 或 /list 命中时由注册表 AddTorrent 并从缓存注入 metainfo。
func (app *App) load() error {
	return app.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(dbBucketInfo))
		if b == nil {
			return nil
		}

		st := b.Stats()

		log.Info().
			Int("cached_metainfo", st.KeyN).
			Int("cache_bytes", st.LeafAlloc).
			Msg("metainfo cache ready (lazy mode: nothing added to client)")

		return nil
	})
}

func (app *App) Client() *torrent.Client {
	return app.client
}

func (app *App) Close() error {
	var err error

	// Remove temporary data folder if required.
	if app.tmp != "" {
		err = os.RemoveAll(app.tmp)
		if err != nil {
			return fmt.Errorf("remove temp. dir: %w", err)
		}
	}

	// Close database.
	if app.db != nil {
		err = app.db.Close()
		if err != nil {
			return fmt.Errorf("close db: %w", err)
		}
	}

	return nil
}

func (app *App) track(t *torrent.Torrent) error {
	if err := app.persist(t); err != nil {
		return err
	}

	t.AddTrackers(trackers)

	return nil
}

// persist 把种子的 metainfo 写入数据库。
// 注意：不持有 app.mu，避免数据库写入阻塞注册表操作。
func (app *App) persist(t *torrent.Torrent) error {
	var mi = t.Metainfo()
	var buf = bytes.NewBuffer(nil)

	if err := mi.Write(buf); err != nil {
		return fmt.Errorf("write metaInfo: %w", err)
	}

	if err := app.db.Update(func(tx *bbolt.Tx) error {
		var b = tx.Bucket([]byte(dbBucketInfo))
		return b.Put(t.InfoHash().Bytes(), buf.Bytes())
	}); err != nil {
		return fmt.Errorf("put to db: %w", err)
	}

	return nil
}
