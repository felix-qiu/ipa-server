package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iineva/ipa-server/pkg/storager"
)

type fakePackage struct {
	name, version, identifier, build string
	size                             int64
}

func (p fakePackage) Name() string       { return p.name }
func (p fakePackage) Version() string    { return p.version }
func (p fakePackage) Identifier() string { return p.identifier }
func (p fakePackage) Build() string      { return p.build }
func (p fakePackage) Channel() string    { return "" }
func (p fakePackage) MetaData() map[string]interface{} {
	return map[string]interface{}{"source": "test"}
}
func (p fakePackage) Icon() image.Image { return nil }
func (p fakePackage) Size() int64       { return p.size }

func newTestService(t *testing.T, store storager.Storager) *service {
	t.Helper()
	db := filepath.Join(t.TempDir(), "test.db")
	srv, err := New(store, "https://apps.example.com", db, "appList.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv.(*service)
}

func addFake(t *testing.T, s *service, projectID string, pkg fakePackage, typ AppInfoType, channel ReleaseChannel, notes string) *ReleaseView {
	t.Helper()
	cleanup := false
	r, err := s.addParsedRelease(projectID, pkg, typ, channel, notes, "", "", "https://apps.example.com", &cleanup)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCreateProjectAndUploadIOSReleases(t *testing.T) {
	s := newTestService(t, storager.NewMemStorager())
	p, err := s.CreateProject("TeleControl", "Remote control")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../../../pkg/ipa/test_data/ipa.ipa")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat, _ := f.Stat()
	first, err := s.UploadRelease(p.ID, f, stat.Size(), AppInfoTypeIpa, ChannelTest, "first test", "https://apps.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if first.Platform != PlatformIOS || first.Channel != ChannelTest {
		t.Fatalf("unexpected release: %+v", first)
	}
	addFake(t, s, p.ID, fakePackage{name: first.Name, version: "2.0", identifier: first.Identifier, build: "20", size: 200}, AppInfoTypeIpa, ChannelTest, "second test")
	view, err := s.GetProject(p.ID, "https://apps.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Applications) != 1 || view.Applications[0].LatestTest.Build != "20" {
		t.Fatalf("application was not reused or latest release is wrong: %+v", view.Applications)
	}
	history, err := s.ListReleases(p.ID, PlatformIOS, ChannelTest, "https://apps.example.com")
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%d err=%v", len(history), err)
	}
}

func TestInspectConfirmAndCancelUpload(t *testing.T) {
	store := storager.NewMemStorager()
	s := newTestService(t, store)
	p, _ := s.CreateProject("Preview", "")
	openFixture := func() (*os.File, int64) {
		f, err := os.Open("../../../pkg/ipa/test_data/ipa.ipa")
		if err != nil {
			t.Fatal(err)
		}
		stat, err := f.Stat()
		if err != nil {
			f.Close()
			t.Fatal(err)
		}
		return f, stat.Size()
	}

	f, size := openFixture()
	preview, err := s.InspectUpload(p.ID, f, size, AppInfoTypeIpa, "client-name.ipa")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if preview.Token == "" || preview.Identifier == "" || preview.Platform != PlatformIOS || preview.FileName != "client-name.ipa" {
		t.Fatalf("invalid preview: %+v", preview)
	}
	release, err := s.ConfirmUpload(p.ID, preview.Token, ChannelTest, "confirmed", "https://apps.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if release.Identifier != preview.Identifier || release.ReleaseNotes != "confirmed" {
		t.Fatalf("preview and release differ: preview=%+v release=%+v", preview, release)
	}
	var pending int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pending_uploads WHERE token=?`, preview.Token).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("confirmed pending upload still exists: count=%d err=%v", pending, err)
	}

	f, size = openFixture()
	cancelPreview, err := s.InspectUpload(p.ID, f, size, AppInfoTypeIpa, "cancel.ipa")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CancelUpload(p.ID, cancelPreview.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenMetadata(filepath.Join(tempDir, cancelPreview.Token)); err == nil {
		t.Fatal("cancelled temporary package still exists")
	}
}

func TestChannelsPromoteWithoutCopy(t *testing.T) {
	s := newTestService(t, storager.NewMemStorager())
	p, _ := s.CreateProject("TeleControl", "")
	r := addFake(t, s, p.ID, fakePackage{name: "TeleControl", version: "1.9", identifier: "com.test.tele", build: "95", size: 10}, AppInfoTypeIpa, ChannelTest, "verified")
	storageName := r.StorageName
	promoted, err := s.PromoteRelease(r.ID, "https://apps.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Channel != ChannelRelease || promoted.StorageName != storageName || promoted.ReleaseNotes != "verified" {
		t.Fatalf("promotion changed immutable data: %+v", promoted)
	}
	test, _ := s.ListReleases(p.ID, PlatformIOS, ChannelTest, "https://apps.example.com")
	release, _ := s.ListReleases(p.ID, PlatformIOS, ChannelRelease, "https://apps.example.com")
	if len(test) != 0 || len(release) != 1 {
		t.Fatalf("channels not separated: TEST=%d RELEASE=%d", len(test), len(release))
	}
}

func TestIdentifierConflictsAndAndroidApplication(t *testing.T) {
	s := newTestService(t, storager.NewMemStorager())
	p1, _ := s.CreateProject("One", "")
	p2, _ := s.CreateProject("Two", "")
	addFake(t, s, p1.ID, fakePackage{name: "One", version: "1", identifier: "com.example.one", build: "1"}, AppInfoTypeIpa, ChannelTest, "")
	cleanup := false
	_, err := s.addParsedRelease(p2.ID, fakePackage{name: "Two", version: "1", identifier: "com.example.one", build: "1"}, AppInfoTypeIpa, ChannelTest, "", "", "", "", &cleanup)
	if !errors.Is(err, ErrIdentifierConflict) || !strings.Contains(err.Error(), "One") {
		t.Fatalf("expected cross-project conflict, got %v", err)
	}
	_, err = s.addParsedRelease(p1.ID, fakePackage{name: "One", version: "2", identifier: "com.example.other", build: "2"}, AppInfoTypeIpa, ChannelTest, "", "", "", "", &cleanup)
	if !errors.Is(err, ErrIdentifierConflict) {
		t.Fatalf("expected same-project platform conflict, got %v", err)
	}
	addFake(t, s, p1.ID, fakePackage{name: "One Android", version: "1", identifier: "com.example.one.android", build: "1"}, AppInfoTypeApk, ChannelRelease, "")
	view, _ := s.GetProject(p1.ID, "")
	if len(view.Applications) != 2 || view.Applications[1].Platform != PlatformAndroid {
		t.Fatalf("android application missing: %+v", view.Applications)
	}
}

func TestPlistNotesAndDelete(t *testing.T) {
	store := storager.NewMemStorager()
	s := newTestService(t, store)
	p, _ := s.CreateProject("Test", "")
	r := addFake(t, s, p.ID, fakePackage{name: "Test", version: "1.0", identifier: "com.example.test", build: "7", size: 12}, AppInfoTypeIpa, ChannelTest, "old")
	updated, err := s.UpdateReleaseNotes(r.ID, "line one\nline two", "https://apps.example.com")
	if err != nil || updated.ReleaseNotes != "line one\nline two" {
		t.Fatalf("notes not updated: %+v %v", updated, err)
	}
	plist, err := s.Plist(r.ID, "https://apps.example.com")
	if err != nil || !bytes.Contains(plist, []byte("com.example.test")) || !bytes.Contains(plist, []byte("com.example.test_1.0")) {
		t.Fatalf("invalid plist: %s err=%v", plist, err)
	}
	if err := s.DeleteRelease(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRelease(r.ID, ""); !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("release still exists: %v", err)
	}
}

func TestSQLitePersistenceAndConcurrentWrites(t *testing.T) {
	store := storager.NewMemStorager()
	dbPath := filepath.Join(t.TempDir(), "persist.db")
	srv, err := New(store, "", dbPath, "appList.json")
	if err != nil {
		t.Fatal(err)
	}
	const count = 20
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := srv.CreateProject("Project "+string(rune('A'+i)), ""); err != nil {
				t.Errorf("create: %v", err)
			}
		}(i)
	}
	wg.Wait()
	_ = srv.Close()
	reopened, err := New(store, "", dbPath, "appList.json")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	projects, err := reopened.ListProjects("")
	if err != nil || len(projects) != count {
		t.Fatalf("persisted projects=%d err=%v", len(projects), err)
	}
}

func TestLegacyMigrationIsIdempotent(t *testing.T) {
	store := storager.NewMemStorager()
	legacy := AppList{
		&AppInfo{ID: "abcdefghijklmnop", Name: "Legacy", Version: "1.0", Identifier: "com.example.legacy", Build: "1", Date: time.Now().Add(-time.Hour), Size: 10, Type: AppInfoTypeIpa, StorageName: "legacy.ipa", MetaData: map[string]interface{}{"old": true}},
		&AppInfo{ID: "qrstuvwxyzABCDEF", Name: "Legacy", Version: "1.1", Identifier: "com.example.legacy", Build: "2", Date: time.Now(), Size: 11, Type: AppInfoTypeIpa, StorageName: "legacy2.ipa"},
		&AppInfo{ID: "androidLegacy001", Name: "Legacy Android", Version: "1.0", Identifier: "com.example.legacy", Build: "3", Date: time.Now(), Size: 12, Type: AppInfoTypeApk, StorageName: "legacy.apk"},
	}
	b, _ := json.Marshal(legacy)
	if err := store.Save("appList.json", bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "migration.db")
	for run := 0; run < 2; run++ {
		srv, err := New(store, "", dbPath, "appList.json")
		if err != nil {
			t.Fatal(err)
		}
		projects, _ := srv.ListProjects("")
		if len(projects) != 2 || len(projects[0].Applications) != 1 || len(projects[1].Applications) != 1 {
			t.Fatalf("run %d duplicate/missing migration: %+v", run, projects)
		}
		var iosProject *ProjectView
		for _, project := range projects {
			if project.Applications[0].Platform == PlatformIOS {
				iosProject = project
			}
		}
		if iosProject == nil {
			t.Fatalf("run %d iOS migration missing: %+v", run, projects)
		}
		releases, _ := srv.ListReleases(iosProject.ID, PlatformIOS, ChannelTest, "")
		if len(releases) != 2 || releases[0].StorageName != "legacy2.ipa" || releases[0].ReleaseNotes != "" {
			t.Fatalf("run %d releases: %+v", run, releases)
		}
		_ = srv.Close()
	}
}
