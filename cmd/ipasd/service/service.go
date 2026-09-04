package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iineva/ipa-server/pkg/apk"
	"github.com/iineva/ipa-server/pkg/ipa"
	"github.com/iineva/ipa-server/pkg/seekbuf"
	"github.com/iineva/ipa-server/pkg/storager"
	"github.com/iineva/ipa-server/pkg/uuid"
)

var (
	ErrIDNotFound          = errors.New("id not found")
	ErrProjectNameRequired = errors.New("project name is required")
	ErrInvalidChannel      = errors.New("channel must be TEST or RELEASE")
	ErrIdentifierConflict  = errors.New("identifier conflict")
)

const tempDir = ".ipa_parser_temp"

type Service interface {
	Close() error
	ListProjects(publicURL string) ([]*ProjectView, error)
	CreateProject(name, description string) (*Project, error)
	GetProject(id, publicURL string) (*ProjectView, error)
	InspectUpload(projectID string, r Reader, size int64, t AppInfoType, fileName string) (*UploadPreview, error)
	ConfirmUpload(projectID, token string, channel ReleaseChannel, notes, publicURL string) (*ReleaseView, error)
	CancelUpload(projectID, token string) error
	UploadRelease(projectID string, r Reader, size int64, t AppInfoType, channel ReleaseChannel, notes, publicURL string) (*ReleaseView, error)
	ListReleases(projectID string, platform Platform, channel ReleaseChannel, publicURL string) ([]*ReleaseView, error)
	GetRelease(id, publicURL string) (*ReleaseView, error)
	UpdateReleaseNotes(id, notes, publicURL string) (*ReleaseView, error)
	PromoteRelease(id, publicURL string) (*ReleaseView, error)
	DeleteRelease(id string) error
	Plist(id, publicURL string) ([]byte, error)
}

type Reader interface {
	io.Reader
	io.ReaderAt
}

type service struct {
	db        *sql.DB
	store     storager.Storager
	publicURL string
}

func New(store storager.Storager, publicURL, databasePath, legacyMetadataName string) (Service, error) {
	dir := filepath.Dir(databasePath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	db, err := openDatabase(databasePath)
	if err != nil {
		return nil, err
	}
	if err := migrateLegacyAppList(db, store, legacyMetadataName); err != nil {
		db.Close()
		return nil, err
	}
	return &service{db: db, store: store, publicURL: publicURL}, nil
}

func (s *service) Close() error { return s.db.Close() }

func (s *service) CreateProject(name, description string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrProjectNameRequired
	}
	now := time.Now().UTC()
	p := &Project{ID: uuid.NewString(), Name: name, Description: strings.TrimSpace(description), CreatedAt: now, UpdatedAt: now}
	_, err := s.db.Exec(`INSERT INTO projects(id,name,description,icon,created_at,updated_at) VALUES(?,?,?,?,?,?)`, p.ID, p.Name, p.Description, p.Icon, formatTime(now), formatTime(now))
	return p, err
}

func (s *service) ListProjects(publicURL string) ([]*ProjectView, error) {
	rows, err := s.db.Query(`SELECT id,name,description,icon,created_at,updated_at FROM projects ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	projects := []*Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	result := []*ProjectView{}
	for _, p := range projects {
		view, err := s.projectView(p, publicURL)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *service) GetProject(id, publicURL string) (*ProjectView, error) {
	p, err := scanProject(s.db.QueryRow(`SELECT id,name,description,icon,created_at,updated_at FROM projects WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIDNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.projectView(p, publicURL)
}

type scanner interface{ Scan(...interface{}) error }

func scanProject(row scanner) (*Project, error) {
	var p Project
	var created, updated string
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Icon, &created, &updated)
	p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)
	return &p, err
}

func (s *service) projectView(p *Project, publicURL string) (*ProjectView, error) {
	view := &ProjectView{Project: *p, Applications: []*ApplicationView{}}
	if p.Icon == "" {
		view.WebIcon = s.servicePublicURL(publicURL, "img/default.png")
	} else {
		view.WebIcon = s.storagerPublicURL(publicURL, p.Icon)
	}
	rows, err := s.db.Query(`SELECT id,project_id,platform,identifier,name,created_at,updated_at FROM applications WHERE project_id=? ORDER BY CASE platform WHEN 'ios' THEN 0 ELSE 1 END`, p.ID)
	if err != nil {
		return nil, err
	}
	apps := []*Application{}
	for rows.Next() {
		app, err := scanApplication(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, app := range apps {
		appView := &ApplicationView{Application: *app}
		appView.LatestTest, err = s.latestRelease(app.ID, ChannelTest, publicURL)
		if err != nil {
			return nil, err
		}
		appView.LatestRelease, err = s.latestRelease(app.ID, ChannelRelease, publicURL)
		if err != nil {
			return nil, err
		}
		view.Applications = append(view.Applications, appView)
	}
	return view, nil
}

func scanApplication(row scanner) (*Application, error) {
	var a Application
	var created, updated string
	err := row.Scan(&a.ID, &a.ProjectID, &a.Platform, &a.Identifier, &a.Name, &created, &updated)
	a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
	return &a, err
}

func (s *service) UploadRelease(projectID string, r Reader, size int64, t AppInfoType, channel ReleaseChannel, notes, publicURL string) (*ReleaseView, error) {
	if !channel.Valid() {
		return nil, ErrInvalidChannel
	}
	if _, err := s.GetProject(projectID, publicURL); err != nil {
		return nil, err
	}
	tempName := filepath.Join(tempDir, uuid.NewString())
	if err := s.store.Save(tempName, r); err != nil {
		return nil, err
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = s.store.Delete(tempName)
		}
	}()
	pkg, err := parsePackage(r, size, t)
	if err != nil {
		return nil, err
	}
	return s.addParsedRelease(projectID, pkg, t, channel, notes, tempName, "", publicURL, &cleanupTemp)
}

func parsePackage(r Reader, size int64, t AppInfoType) (Package, error) {
	switch t {
	case AppInfoTypeIpa:
		return ipa.Parse(r, size)
	case AppInfoTypeApk:
		return apk.Parse(r, size)
	default:
		return nil, errors.New("unsupported package type")
	}
}

func (s *service) InspectUpload(projectID string, r Reader, size int64, t AppInfoType, fileName string) (*UploadPreview, error) {
	if _, err := s.GetProject(projectID, ""); err != nil {
		return nil, err
	}
	token := uuid.NewString()
	tempName := filepath.Join(tempDir, token)
	if err := s.store.Save(tempName, r); err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = s.store.Delete(tempName)
		}
	}()
	pkg, err := parsePackage(r, size, t)
	if err != nil {
		return nil, err
	}
	if err := s.validatePackageProject(s.db, projectID, platformFromType(t), pkg.Identifier()); err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`INSERT INTO pending_uploads(token,project_id,storage_name,size,package_type,file_name,created_at) VALUES(?,?,?,?,?,?,?)`, token, projectID, tempName, size, t, filepath.Base(fileName), formatTime(time.Now().UTC()))
	if err != nil {
		return nil, err
	}
	keep = true
	return &UploadPreview{Token: token, FileName: filepath.Base(fileName), Name: pkg.Name(), Platform: platformFromType(t), Identifier: pkg.Identifier(), Version: pkg.Version(), Build: pkg.Build(), Size: pkg.Size(), Type: t}, nil
}

func (s *service) ConfirmUpload(projectID, token string, channel ReleaseChannel, notes, publicURL string) (*ReleaseView, error) {
	if !channel.Valid() {
		return nil, ErrInvalidChannel
	}
	var tempName string
	var size int64
	var t AppInfoType
	err := s.db.QueryRow(`SELECT storage_name,size,package_type FROM pending_uploads WHERE token=? AND project_id=?`, token, projectID).Scan(&tempName, &size, &t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIDNotFound
	}
	if err != nil {
		return nil, err
	}
	f, err := s.store.OpenMetadata(tempName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf, err := seekbuf.Open(f, seekbuf.FileMode)
	if err != nil {
		return nil, err
	}
	defer buf.Close()
	pkg, err := parsePackage(buf, size, t)
	if err != nil {
		return nil, err
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = s.store.Delete(tempName)
		}
	}()
	return s.addParsedRelease(projectID, pkg, t, channel, notes, tempName, token, publicURL, &cleanupTemp)
}

func (s *service) CancelUpload(projectID, token string) error {
	var tempName string
	err := s.db.QueryRow(`SELECT storage_name FROM pending_uploads WHERE token=? AND project_id=?`, token, projectID).Scan(&tempName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIDNotFound
	}
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`DELETE FROM pending_uploads WHERE token=? AND project_id=?`, token, projectID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrIDNotFound
	}
	if err := s.store.Delete(tempName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type queryRower interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

func (s *service) validatePackageProject(q queryRower, projectID string, platform Platform, identifier string) error {
	var ownerProjectID, ownerProjectName string
	err := q.QueryRow(`SELECT a.project_id,p.name FROM applications a JOIN projects p ON p.id=a.project_id WHERE a.identifier=?`, identifier).Scan(&ownerProjectID, &ownerProjectName)
	if err == nil && ownerProjectID != projectID {
		return fmt.Errorf("%w: 该应用已经属于项目「%s」。Identifier：%s", ErrIdentifierConflict, ownerProjectName, identifier)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var existingIdentifier string
	err = q.QueryRow(`SELECT identifier FROM applications WHERE project_id=? AND platform=?`, projectID, platform).Scan(&existingIdentifier)
	if err == nil && existingIdentifier != identifier {
		return fmt.Errorf("%w: 项目当前 %s 应用的 Identifier 为 %s，不能上传 %s", ErrIdentifierConflict, platform, existingIdentifier, identifier)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func (s *service) addParsedRelease(projectID string, pkg Package, t AppInfoType, channel ReleaseChannel, notes, tempName, pendingToken, publicURL string, cleanupTemp *bool) (*ReleaseView, error) {
	platform := platformFromType(t)
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := s.validatePackageProject(tx, projectID, platform, pkg.Identifier()); err != nil {
		return nil, err
	}
	var appID, identifier string
	err = tx.QueryRow(`SELECT id,identifier FROM applications WHERE project_id=? AND platform=?`, projectID, platform).Scan(&appID, &identifier)
	if err == nil && identifier != pkg.Identifier() {
		return nil, fmt.Errorf("%w: 项目当前 %s 应用的 Identifier 为 %s，不能上传 %s", ErrIdentifierConflict, platform, identifier, pkg.Identifier())
	}
	if errors.Is(err, sql.ErrNoRows) {
		appID = uuid.NewString()
		if _, err := tx.Exec(`INSERT INTO applications(id,project_id,platform,identifier,name,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, appID, projectID, platform, pkg.Identifier(), pkg.Name(), formatTime(now), formatTime(now)); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE applications SET name=?,updated_at=? WHERE id=?`, pkg.Name(), formatTime(now), appID); err != nil {
		return nil, err
	}
	releaseID := uuid.NewString()
	storageName := fmt.Sprintf("%s_%s(%s)_%s_%s%s", pkg.Identifier(), pkg.Version(), pkg.Build(), channel, releaseID, t.StorageName())
	iconName := ""
	if pkg.Icon() != nil {
		iconName = filepath.Join(pkg.Identifier(), releaseID+".png")
	}
	meta, err := json.Marshal(pkg.MetaData())
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO releases(id,application_id,version,build,channel,storage_name,size,icon,release_notes,metadata,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, releaseID, appID, pkg.Version(), pkg.Build(), channel, storageName, pkg.Size(), iconName, notes, string(meta), formatTime(now), formatTime(now)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE projects SET icon=CASE WHEN icon='' THEN ? ELSE icon END,updated_at=? WHERE id=?`, iconName, formatTime(now), projectID); err != nil {
		return nil, err
	}
	if pendingToken != "" {
		result, err := tx.Exec(`DELETE FROM pending_uploads WHERE token=? AND project_id=?`, pendingToken, projectID)
		if err != nil {
			return nil, err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return nil, ErrIDNotFound
		}
	}
	if tempName != "" {
		if err := s.store.Move(tempName, storageName); err != nil {
			return nil, err
		}
		*cleanupTemp = false
	}
	if iconName != "" {
		buf := &bytes.Buffer{}
		if err := png.Encode(buf, pkg.Icon()); err == nil {
			if err := s.store.Save(iconName, buf); err != nil {
				_ = s.store.Delete(storageName)
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		_ = s.store.Delete(storageName)
		if iconName != "" {
			_ = s.store.Delete(iconName)
		}
		return nil, err
	}
	return s.GetRelease(releaseID, publicURL)
}

func (s *service) ListReleases(projectID string, platform Platform, channel ReleaseChannel, publicURL string) ([]*ReleaseView, error) {
	if !platform.Valid() || !channel.Valid() {
		return nil, errors.New("invalid platform or channel")
	}
	rows, err := s.db.Query(releaseSelect+` WHERE a.project_id=? AND a.platform=? AND r.channel=? ORDER BY r.created_at DESC`, projectID, platform, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []*ReleaseView{}
	for rows.Next() {
		v, err := s.scanReleaseView(rows, publicURL)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

const releaseSelect = `SELECT r.id,r.application_id,r.version,r.build,r.channel,r.storage_name,r.size,r.icon,r.release_notes,r.metadata,r.created_at,r.updated_at,a.project_id,a.platform,a.identifier,a.name FROM releases r JOIN applications a ON a.id=r.application_id`

func (s *service) GetRelease(id, publicURL string) (*ReleaseView, error) {
	v, err := s.scanReleaseView(s.db.QueryRow(releaseSelect+` WHERE r.id=?`, id), publicURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIDNotFound
	}
	return v, err
}

func (s *service) latestRelease(appID string, channel ReleaseChannel, publicURL string) (*ReleaseView, error) {
	v, err := s.scanReleaseView(s.db.QueryRow(releaseSelect+` WHERE r.application_id=? AND r.channel=? ORDER BY r.created_at DESC LIMIT 1`, appID, channel), publicURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func (s *service) scanReleaseView(row scanner, publicURL string) (*ReleaseView, error) {
	var v ReleaseView
	var meta, created, updated string
	err := row.Scan(&v.ID, &v.ApplicationID, &v.Version, &v.Build, &v.Channel, &v.StorageName, &v.Size, &v.Icon, &v.ReleaseNotes, &meta, &created, &updated, &v.ProjectID, &v.Platform, &v.Identifier, &v.Name)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(meta), &v.MetaData)
	if v.MetaData == nil {
		v.MetaData = map[string]interface{}{}
	}
	v.CreatedAt, v.UpdatedAt = parseTime(created), parseTime(updated)
	v.PackageURL = s.storagerPublicURL(publicURL, v.StorageName)
	if v.Icon == "" {
		v.WebIcon = s.servicePublicURL(publicURL, "img/default.png")
	} else {
		v.WebIcon = s.storagerPublicURL(publicURL, v.Icon)
	}
	if v.Platform == PlatformIOS {
		v.PlistURL = s.servicePublicURL(publicURL, fmt.Sprintf("plist/%s.plist", v.ID))
		v.InstallURL = "itms-services://?action=download-manifest&url=" + url.QueryEscape(v.PlistURL)
	} else {
		v.InstallURL = v.PackageURL
	}
	return &v, nil
}

func (s *service) UpdateReleaseNotes(id, notes, publicURL string) (*ReleaseView, error) {
	result, err := s.db.Exec(`UPDATE releases SET release_notes=?,updated_at=? WHERE id=?`, notes, formatTime(time.Now().UTC()), id)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, ErrIDNotFound
	}
	return s.GetRelease(id, publicURL)
}

func (s *service) PromoteRelease(id, publicURL string) (*ReleaseView, error) {
	now := formatTime(time.Now().UTC())
	result, err := s.db.Exec(`UPDATE releases SET channel='RELEASE',updated_at=? WHERE id=? AND channel='TEST'`, now, id)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		if _, err := s.GetRelease(id, publicURL); err != nil {
			return nil, err
		}
		return nil, errors.New("only TEST releases can be promoted")
	}
	var projectID string
	_ = s.db.QueryRow(`SELECT a.project_id FROM releases r JOIN applications a ON a.id=r.application_id WHERE r.id=?`, id).Scan(&projectID)
	_, _ = s.db.Exec(`UPDATE projects SET updated_at=? WHERE id=?`, now, projectID)
	return s.GetRelease(id, publicURL)
}

func (s *service) DeleteRelease(id string) error {
	v, err := s.GetRelease(id, "")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM releases WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE projects SET icon=CASE WHEN icon=? THEN COALESCE((SELECT r.icon FROM releases r JOIN applications a ON a.id=r.application_id WHERE a.project_id=? AND r.icon<>'' ORDER BY r.created_at DESC LIMIT 1),'') ELSE icon END,updated_at=? WHERE id=?`, v.Icon, v.ProjectID, formatTime(time.Now().UTC()), v.ProjectID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.store.Delete(v.StorageName); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if v.Icon != "" {
		if err := s.store.Delete(v.Icon); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *service) Plist(id, publicURL string) ([]byte, error) {
	v, err := s.GetRelease(id, publicURL)
	if err != nil {
		return nil, err
	}
	if v.Platform != PlatformIOS {
		return nil, errors.New("plist is only available for iOS releases")
	}
	return NewInstallPlist(&Item{Name: v.Name, Version: v.Version, Identifier: v.Identifier, Icon: v.WebIcon, Pkg: v.PackageURL})
}

func (s *service) storagerPublicURL(publicURL, name string) string {
	if s.publicURL != "" {
		publicURL = s.publicURL
	}
	u, err := s.store.PublicURL(publicURL, name)
	if err != nil {
		return ""
	}
	return u
}

func (s *service) servicePublicURL(publicURL, name string) string {
	if s.publicURL != "" {
		publicURL = s.publicURL
	}
	u, err := url.Parse(publicURL)
	if err != nil {
		return ""
	}
	u.Path = filepath.Join(u.Path, name)
	return u.String()
}

type ServiceMiddleware func(Service) Service
