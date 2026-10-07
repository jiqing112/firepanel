// Package store：SQLite 持久化层（期望状态存储）。
// 面板不存裸命令字符串，只存结构化规则；内核规则由 fw 层按后端动态生成。
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store 封装数据库连接与仓库方法。
type Store struct {
	db *sql.DB
}

// Open 打开（必要时创建）数据库并执行迁移。
func Open(path string) (*Store, error) {
	// modernc/sqlite: 单写多读，限制连接数避免 SQLITE_BUSY
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

type migration struct {
	version int
	name    string
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now','localtime')))`); err != nil {
		return fmt.Errorf("创建迁移表: %w", err)
	}
	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("查询迁移版本: %w", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("读取内嵌迁移: %w", err)
	}
	// 文件名形如 001_init.sql，按版本号升序执行
	var names []string
	versions := map[string]int{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := e.Name()
		dot := strings.IndexByte(base, '_')
		if dot <= 0 {
			continue
		}
		v, err := strconv.Atoi(base[:dot])
		if err != nil {
			continue
		}
		names = append(names, base)
		versions[base] = v
	}
	sort.Slice(names, func(i, j int) bool { return versions[names[i]] < versions[names[j]] })

	for _, name := range names {
		version := versions[name]
		if version <= current {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s: %w", name, err)
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("执行迁移 %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,name) VALUES(?,?)`, version, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("记录迁移 %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
