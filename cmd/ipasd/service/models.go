package service

import "time"

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

func (p Platform) Valid() bool { return p == PlatformIOS || p == PlatformAndroid }

type ReleaseChannel string

const (
	ChannelTest    ReleaseChannel = "TEST"
	ChannelRelease ReleaseChannel = "RELEASE"
)

func (c ReleaseChannel) Valid() bool { return c == ChannelTest || c == ChannelRelease }

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Application struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"projectId"`
	Platform   Platform  `json:"platform"`
	Identifier string    `json:"identifier"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Release struct {
	ID            string                 `json:"id"`
	ApplicationID string                 `json:"applicationId"`
	Version       string                 `json:"version"`
	Build         string                 `json:"build"`
	Channel       ReleaseChannel         `json:"channel"`
	StorageName   string                 `json:"storageName"`
	Size          int64                  `json:"size"`
	Icon          string                 `json:"icon"`
	ReleaseNotes  string                 `json:"releaseNotes"`
	MetaData      map[string]interface{} `json:"metaData"`
	CreatedAt     time.Time              `json:"createdAt"`
	UpdatedAt     time.Time              `json:"updatedAt"`
}

type ReleaseView struct {
	Release
	ProjectID  string   `json:"projectId"`
	Platform   Platform `json:"platform"`
	Identifier string   `json:"identifier"`
	Name       string   `json:"name"`
	PackageURL string   `json:"packageUrl"`
	PlistURL   string   `json:"plistUrl,omitempty"`
	InstallURL string   `json:"installUrl"`
	WebIcon    string   `json:"webIcon"`
}

type ApplicationView struct {
	Application
	LatestTest    *ReleaseView `json:"latestTest,omitempty"`
	LatestRelease *ReleaseView `json:"latestRelease,omitempty"`
}

type ProjectView struct {
	Project
	WebIcon      string             `json:"webIcon"`
	Applications []*ApplicationView `json:"applications"`
}

type UploadPreview struct {
	Token      string      `json:"token"`
	FileName   string      `json:"fileName"`
	Name       string      `json:"name"`
	Platform   Platform    `json:"platform"`
	Identifier string      `json:"identifier"`
	Version    string      `json:"version"`
	Build      string      `json:"build"`
	Size       int64       `json:"size"`
	Type       AppInfoType `json:"type"`
}

// Item is retained as the small plist template contract and for source
// compatibility with callers that used the former AppInfo response type.
type Item struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Identifier string `json:"identifier"`
	Icon       string `json:"icon"`
	Pkg        string `json:"pkg"`
}
