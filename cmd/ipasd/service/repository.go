package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"time"

	"github.com/iineva/ipa-server/pkg/storager"
	"github.com/iineva/ipa-server/pkg/uuid"
	_ "modernc.org/sqlite"
)

const schemaVersion = "3"

const schemaSQL = `
CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  icon TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS applications (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  platform TEXT NOT NULL CHECK(platform IN ('ios', 'android')),
  identifier TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(project_id, platform),
  UNIQUE(identifier, platform)
);
CREATE TABLE IF NOT EXISTS releases (
  id TEXT PRIMARY KEY,
  application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
  version TEXT NOT NULL,
  build TEXT NOT NULL,
  channel TEXT NOT NULL CHECK(channel IN ('TEST', 'RELEASE')),
  storage_name TEXT NOT NULL,
  size INTEGER NOT NULL,
  icon TEXT NOT NULL DEFAULT '',
  release_notes TEXT NOT NULL DEFAULT '',
  metadata TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS releases_application_channel_created
  ON releases(application_id, channel, created_at DESC);
CREATE TABLE IF NOT EXISTS pending_uploads (
  token TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  storage_name TEXT NOT NULL,
  size INTEGER NOT NULL,
  package_type INTEGER NOT NULL,
  file_name TEXT NOT NULL,
  created_at TEXT NOT NULL
);
`

func openDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL;"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`INSERT INTO schema_meta(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrateLegacyAppList(db *sql.DB, store storager.Storager, metadataName string) error {
	var done string
	err := db.QueryRow(`SELECT value FROM schema_meta WHERE key='legacy_app_list_migrated'`).Scan(&done)
	if err == nil && done == "1" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	f, err := store.OpenMetadata(metadataName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, markErr := db.Exec(`INSERT INTO schema_meta(key,value) VALUES('legacy_app_list_migrated','1') ON CONFLICT(key) DO UPDATE SET value='1'`)
			return markErr
		}
		// Remote backends do not expose a common not-found error. Leave the marker
		// unset so a transiently unavailable legacy file is retried next start.
		return nil
	}
	defer f.Close()
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}
	var list AppList
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("decode legacy metadata: %w", err)
	}
	sort.Sort(list)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	projectByLegacyApp := map[string]string{}
	applicationByLegacyApp := map[string]string{}
	for i := len(list) - 1; i >= 0; i-- {
		old := list[i]
		if old == nil || old.Identifier == "" || old.ID == "" {
			continue
		}
		created := old.Date
		if created.IsZero() {
			created = time.Now().UTC()
		}
		legacyKey := fmt.Sprintf("%s:%d", old.Identifier, old.Type)
		projectID, ok := projectByLegacyApp[legacyKey]
		if !ok {
			projectID = uuid.NewString()
			if _, err := tx.Exec(`INSERT INTO projects(id,name,description,icon,created_at,updated_at) VALUES(?,?,?,?,?,?)`, projectID, old.Name, "", old.IconStorageName(), formatTime(created), formatTime(created)); err != nil {
				return err
			}
			projectByLegacyApp[legacyKey] = projectID
		}
		appID, ok := applicationByLegacyApp[legacyKey]
		if !ok {
			appID = uuid.NewString()
			if _, err := tx.Exec(`INSERT INTO applications(id,project_id,platform,identifier,name,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, appID, projectID, platformFromType(old.Type), old.Identifier, old.Name, formatTime(created), formatTime(created)); err != nil {
				return err
			}
			applicationByLegacyApp[legacyKey] = appID
		}
		meta, _ := json.Marshal(old.MetaData)
		if _, err := tx.Exec(`INSERT OR IGNORE INTO releases(id,application_id,version,build,channel,storage_name,size,icon,release_notes,metadata,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, old.ID, appID, old.Version, old.Build, ChannelTest, old.PackageStorageName(), old.Size, old.IconStorageName(), "", string(meta), formatTime(created), formatTime(created)); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE projects SET updated_at=?,icon=CASE WHEN icon='' THEN ? ELSE icon END WHERE id=?`, formatTime(created), old.IconStorageName(), projectID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE applications SET updated_at=? WHERE id=?`, formatTime(created), appID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_meta(key,value) VALUES('legacy_app_list_migrated','1') ON CONFLICT(key) DO UPDATE SET value='1'`); err != nil {
		return err
	}
	return tx.Commit()
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

func platformFromType(t AppInfoType) Platform {
	if t == AppInfoTypeApk {
		return PlatformAndroid
	}
	return PlatformIOS
}
