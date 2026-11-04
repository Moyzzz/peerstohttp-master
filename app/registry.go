package app

// 本文件实现种子的生命周期管理：引用计数 + 两段式回收。
//
// 注册表是创建种子的唯一入口：/content 与 /list 都通过 Acquire / AcquireMagnet 取种子，
// 用完必须调用 release；启动时不再预加载历史种子（见 App.load）。
//
// 两段式回收：
//  1. 最后一个请求结束（refs 归零）时，先把该种子的最大连接数置零，
//     立即断开全部 peer 连接、释放连接缓冲，但保留种子对象与分片状态；
//  2. 宽限期（-idle-ttl，默认 90 秒）内没有新请求命中，才真正 Drop()
//     把种子从 client 中移除，释放按分片数计算的元数据。
//     数据库中的 metainfo 默认保留（-keep-meta=true），以便再次访问时秒出 info。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/rs/zerolog/log"
	"go.etcd.io/bbolt"
)

// ErrInfoTimeout 表示在 -info-timeout 内没有拿到种子元数据。
var ErrInfoTimeout = errors.New("torrent: metainfo timeout")

// trackedTorrent 是注册表中的一项。
type trackedTorrent struct {
	t        *torrent.Torrent
	refs     int
	lastUsed time.Time
	// evict 是 refs 归零后到 Drop 之间的宽限定时器（阶段二）。
	evict *time.Timer
	// cut 是 refs 归零后到"断开全部 peer 连接"之间的宽限定时器（阶段一）。
	// 播放器缓冲满了会主动中止请求、稍后再发新 Range，若一归零就断连，
	// 每次间隙都要重新拨号，看起来就是"变慢"。
	cut *time.Timer
	// capped 表示当前处于"阶段一"：连接上限已被置零。
	capped bool

	// ready 在首次创建流程结束时关闭。
	ready chan struct{}
	// err 在 close(ready) 之后可读，非 nil 表示创建失败。
	err error
}

// Acquire 按 infoHash 取得种子并使其引用计数加一。调用方必须调用返回的 release。
func (app *App) Acquire(ctx context.Context, hash metainfo.Hash) (*torrent.Torrent, func(), error) {
	return app.acquire(ctx, hash, func() (*torrent.Torrent, bool, error) {
		t, isNew := app.client.AddTorrentInfoHash(hash)
		return t, isNew, nil
	})
}

// AcquireMagnet 按 magnet 取得种子并使其引用计数加一。
// 首次创建时会带上 magnet 里的 trackers / display name / web seeds / peer addrs。
func (app *App) AcquireMagnet(ctx context.Context, m *metainfo.Magnet) (*torrent.Torrent, func(), error) {
	return app.acquire(ctx, m.InfoHash, func() (*torrent.Torrent, bool, error) {
		spec, err := torrent.TorrentSpecFromMagnetUri(m.String())
		if err != nil {
			return nil, false, err
		}

		return app.client.AddTorrentSpec(spec)
	})
}

// acquire 是 /content 与 /list 的公共实现：命中的话只加引用，未命中则由 apply 创建一次。
//
// 同一个 infoHash 的并发或连续请求共享同一个 *torrent.Torrent：
// release 只做减计数，真正断开与移除由两段式回收负责。
func (app *App) acquire(
	ctx context.Context,
	hash metainfo.Hash,
	apply func() (*torrent.Torrent, bool, error),
) (*torrent.Torrent, func(), error) {
	app.mu.Lock()

	if e, ok := app.active[hash]; ok {
		e.refs++
		e.lastUsed = time.Now()

		if e.evict != nil {
			e.evict.Stop()
			e.evict = nil
		}

		if e.cut != nil {
			e.cut.Stop()
			e.cut = nil
		}

		// 阶段一可逆：有新请求回来就恢复连接上限，不必重建对象。
		if e.t != nil && e.capped {
			e.t.SetMaxEstablishedConns(app.maxConns)
			e.capped = false
		}

		app.mu.Unlock()

		// 可能是别的请求正在创建，等它收尾。
		select {
		case <-e.ready:
		case <-ctx.Done():
			app.release(hash)
			return nil, nil, ctx.Err()
		}

		if e.err != nil {
			app.release(hash)
			return nil, nil, e.err
		}

		return e.t, app.releaseFunc(hash), nil
	}

	// 占位，让并发到达的请求共用同一次创建流程。
	e := &trackedTorrent{
		refs:     1,
		lastUsed: time.Now(),
		ready:    make(chan struct{}),
	}
	app.active[hash] = e
	app.mu.Unlock()

	t, err := app.createTorrent(ctx, hash, apply)

	app.mu.Lock()
	if err != nil {
		delete(app.active, hash)
		e.err = err
	} else {
		e.t = t
	}
	app.mu.Unlock()
	close(e.ready) // 先写后 close，等待方通过 channel 建立 happens-before

	if err != nil {
		return nil, nil, err
	}

	return t, app.releaseFunc(hash), nil
}

// releaseFunc 包装 release，避免 defer 与错误分支重复调用导致计数变负。
func (app *App) releaseFunc(hash metainfo.Hash) func() {
	var once sync.Once
	return func() { once.Do(func() { app.release(hash) }) }
}

// release 减少引用计数；归零时安排两段式回收。
func (app *App) release(hash metainfo.Hash) {
	app.mu.Lock()
	defer app.mu.Unlock()

	e, ok := app.active[hash]
	if !ok {
		return
	}

	e.refs--
	e.lastUsed = time.Now()
	if e.refs > 0 {
		return
	}

	// 阶段一：宽限 disconnectTTL 后再断开全部 peer 连接。
	// 播放器缓冲满时会主动中止请求、稍后重发 Range，若一归零就断连，
	// 每个间隙都要重新拨号与重排分片优先级，表现为"下载变慢"。
	if e.t != nil && !e.capped && e.cut == nil {
		if app.disconnectTTL <= 0 {
			app.cutConnsLocked(e, hash)
		} else {
			e.cut = time.AfterFunc(app.disconnectTTL, func() { app.cutConns(hash) })
		}
	}

	// 阶段二：空闲 idleTTL 后移除种子记录。
	e.evict = time.AfterFunc(app.idleTTL, func() { app.drop(hash) })
}

// cutConns 在宽限期结束后断开全部 peer 连接（阶段一）。
func (app *App) cutConns(hash metainfo.Hash) {
	app.mu.Lock()
	defer app.mu.Unlock()

	e, ok := app.active[hash]
	if !ok || e.refs > 0 {
		return
	}

	e.cut = nil

	if e.t == nil || e.capped {
		return
	}

	app.cutConnsLocked(e, hash)
}

// cutConnsLocked 要求调用方已持有 app.mu。
func (app *App) cutConnsLocked(e *trackedTorrent, hash metainfo.Hash) {
	e.t.SetMaxEstablishedConns(0)
	e.capped = true

	log.Info().
		Str("hash", hash.HexString()).
		Bool("keep_meta", app.keepMeta).
		Msg("torrent idle: peer connections closed, waiting for grace period")
}

// drop 在宽限期结束后执行阶段二：把种子从 client 与本地索引中移除。
func (app *App) drop(hash metainfo.Hash) {
	app.mu.Lock()

	e, ok := app.active[hash]
	if !ok || e.refs > 0 || e.t == nil {
		app.mu.Unlock()
		return
	}

	delete(app.active, hash)

	if e.cut != nil {
		e.cut.Stop()
		e.cut = nil
	}

	// 持锁 Drop：避免"新请求刚拿到指针、旧 goroutine 正在 Drop"的悬垂对象。
	// 锁顺序始终为 app.mu -> torrent client 内部锁。
	e.t.Drop()

	app.mu.Unlock()

	log.Info().
		Str("hash", hash.HexString()).
		Msg("torrent dropped after idle grace period")

	if app.keepMeta {
		return
	}

	// 仅在明确要求"彻底遗忘"时删除元数据缓存。
	err := app.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(dbBucketInfo)).Delete(hash.Bytes())
	})
	if err != nil {
		log.Warn().Err(err).Str("hash", hash.HexString()).Msg("delete torrent metainfo")
	}
}

// createTorrent 负责 Add + 注入缓存的 metainfo + 带超时等待 info + 落库。
// 全程不持有 app.mu，因为等待元数据可能阻塞数秒。
// apply 由调用方提供（按 hash 或按 magnet），只在真正创建时执行一次。
func (app *App) createTorrent(
	ctx context.Context,
	hash metainfo.Hash,
	apply func() (*torrent.Torrent, bool, error),
) (*torrent.Torrent, error) {
	t, isNew, err := apply()
	if err != nil {
		return nil, fmt.Errorf("torrent: add %s: %w", hash.HexString(), err)
	}

	if t == nil {
		return nil, fmt.Errorf("torrent: add %s returned nil", hash.HexString())
	}

	if isNew {
		log.Info().Str("hash", hash.HexString()).Msg("torrent created")
	}

	// 数据库里有 metainfo 就直接注入，省掉一次 metadata 交换。
	if mi, ok := app.loadMetaInfo(hash); ok {
		if err := t.SetInfoBytes(mi.InfoBytes); err != nil {
			log.Warn().Err(err).Str("hash", hash.HexString()).
				Msg("inject cached metainfo failed, falling back to metadata exchange")
		}
	}

	timer := time.NewTimer(app.infoTimeout)
	defer timer.Stop()

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		// 只有本次调用新建的对象才能回收，避免误杀 /list 或启动加载的种子。
		if isNew {
			t.Drop()
		}
		return nil, ctx.Err()
	case <-timer.C:
		if isNew {
			t.Drop()
		}
		return nil, ErrInfoTimeout
	}

	if err := app.track(t); err != nil {
		log.Warn().Err(err).Str("hash", hash.HexString()).Msg("track torrent")
	}

	return t, nil
}

// CachedInfo 只读地查一次元数据缓存：先内存，再 bolt。
//
// 返回 (info, 来源, 是否命中)，来源取值 "HIT-MEM" / "HIT-DISK" / "MISS"。
// 它不会创建种子、不改变引用计数、也不触发任何网络行为，
// 因此可以安全地用于 HEAD 这类"只探测、不下载"的请求。
func (app *App) CachedInfo(hash metainfo.Hash) (*metainfo.Info, string, bool) {
	app.mu.RLock()
	if e, ok := app.active[hash]; ok && e.t != nil {
		if info := e.t.Info(); info != nil {
			app.mu.RUnlock()
			return info, "HIT-MEM", true
		}
	}
	app.mu.RUnlock()

	mi, ok := app.loadMetaInfo(hash)
	if !ok {
		return nil, "MISS", false
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		log.Warn().Err(err).Str("hash", hash.HexString()).Msg("unmarshal cached metainfo")
		return nil, "MISS", false
	}

	return &info, "HIT-DISK", true
}

// loadMetaInfo 从数据库读取并解析已缓存的 metainfo。
func (app *App) loadMetaInfo(hash metainfo.Hash) (*metainfo.MetaInfo, bool) {
	var raw []byte

	err := app.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(dbBucketInfo))
		if b == nil {
			return nil
		}

		v := b.Get(hash.Bytes())
		if v == nil {
			return nil
		}

		// bolt 返回的切片只在事务内有效，必须拷贝。
		raw = append([]byte(nil), v...)

		return nil
	})
	if err != nil || raw == nil {
		return nil, false
	}

	mi, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil {
		log.Warn().Err(err).Str("hash", hash.HexString()).Msg("load cached metainfo")
		return nil, false
	}

	return mi, true
}
