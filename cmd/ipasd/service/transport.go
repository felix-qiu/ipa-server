package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/iineva/ipa-server/pkg/common"
	"github.com/iineva/ipa-server/pkg/seekbuf"
)

type APIHandler struct {
	srv           Service
	uploadEnabled bool
	deleteEnabled bool
}

func NewAPIHandler(srv Service, uploadEnabled, deleteEnabled bool) http.Handler {
	return &APIHandler{srv: srv, uploadEnabled: uploadEnabled, deleteEnabled: deleteEnabled}
}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch {
	case r.URL.Path == "/api/projects":
		h.projects(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/projects/"):
		h.project(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/releases/"):
		h.release(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/info/") && r.Method == http.MethodGet:
		id := path.Base(r.URL.Path)
		v, err := h.srv.GetRelease(id, publicURL(r))
		h.write(w, v, err, http.StatusOK)
	case r.URL.Path == "/api/list" && r.Method == http.MethodGet:
		projects, err := h.srv.ListProjects(publicURL(r))
		h.write(w, map[string]interface{}{"projects": projects, "uploadEnabled": h.uploadEnabled, "deleteEnabled": h.deleteEnabled}, err, http.StatusOK)
	case r.URL.Path == "/api/delete/get" && r.Method == http.MethodGet:
		h.write(w, map[string]bool{"delete": h.deleteEnabled}, nil, http.StatusOK)
	case r.URL.Path == "/api/delete" && r.Method == http.MethodPost:
		if !h.deleteEnabled {
			h.write(w, nil, errors.New("no permission to delete"), http.StatusForbidden)
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		if err == nil {
			err = h.srv.DeleteRelease(body.ID)
		}
		h.write(w, map[string]string{"msg": "ok"}, err, http.StatusOK)
	default:
		h.write(w, nil, errors.New("not found"), http.StatusNotFound)
	}
}

func (h *APIHandler) projects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projects, err := h.srv.ListProjects(publicURL(r))
		h.write(w, map[string]interface{}{"projects": projects, "uploadEnabled": h.uploadEnabled, "deleteEnabled": h.deleteEnabled}, err, http.StatusOK)
	case http.MethodPost:
		if !h.uploadEnabled {
			h.write(w, nil, errors.New("project creation was disabled"), http.StatusForbidden)
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil {
			h.write(w, nil, err, http.StatusBadRequest)
			return
		}
		p, err := h.srv.CreateProject(body.Name, body.Description)
		h.write(w, p, err, http.StatusCreated)
	default:
		h.write(w, nil, errors.New("method not allowed"), http.StatusMethodNotAllowed)
	}
}

func (h *APIHandler) project(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/projects/"))
	if len(parts) == 0 {
		h.write(w, nil, errors.New("not found"), http.StatusNotFound)
		return
	}
	projectID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		p, err := h.srv.GetProject(projectID, publicURL(r))
		h.write(w, map[string]interface{}{"project": p, "uploadEnabled": h.uploadEnabled, "deleteEnabled": h.deleteEnabled}, err, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "releases" && r.Method == http.MethodGet {
		platform := Platform(strings.ToLower(r.URL.Query().Get("platform")))
		channel := ReleaseChannel(strings.ToUpper(r.URL.Query().Get("channel")))
		list, err := h.srv.ListReleases(projectID, platform, channel, publicURL(r))
		h.write(w, map[string]interface{}{"releases": list}, err, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "upload" && r.Method == http.MethodPost {
		if !h.uploadEnabled {
			h.write(w, nil, errors.New("upload was disabled"), http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			h.upload(w, r, projectID)
		} else {
			h.confirmUpload(w, r, projectID)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "inspect" && r.Method == http.MethodPost {
		if !h.uploadEnabled {
			h.write(w, nil, errors.New("upload was disabled"), http.StatusForbidden)
			return
		}
		h.inspectUpload(w, r, projectID)
		return
	}
	if len(parts) == 3 && parts[1] == "uploads" && r.Method == http.MethodDelete {
		if !h.uploadEnabled {
			h.write(w, nil, errors.New("upload was disabled"), http.StatusForbidden)
			return
		}
		h.write(w, map[string]string{"msg": "ok"}, h.srv.CancelUpload(projectID, parts[2]), http.StatusOK)
		return
	}
	h.write(w, nil, errors.New("not found"), http.StatusNotFound)
}

func (h *APIHandler) inspectUpload(w http.ResponseWriter, r *http.Request, projectID string) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.write(w, nil, err, http.StatusBadRequest)
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		h.write(w, nil, err, http.StatusBadRequest)
		return
	}
	defer f.Close()
	t := FileType(header.Filename)
	if t == AppInfoTypeUnknown {
		h.write(w, nil, fmt.Errorf("do not support %s file", path.Ext(header.Filename)), http.StatusBadRequest)
		return
	}
	buf, err := seekbuf.Open(f, seekbuf.FileMode)
	if err != nil {
		h.write(w, nil, err, http.StatusBadRequest)
		return
	}
	defer buf.Close()
	preview, err := h.srv.InspectUpload(projectID, buf, header.Size, t, header.Filename)
	h.write(w, map[string]interface{}{"preview": preview}, err, http.StatusCreated)
}

func (h *APIHandler) confirmUpload(w http.ResponseWriter, r *http.Request, projectID string) {
	var body struct {
		Token        string         `json:"token"`
		Channel      ReleaseChannel `json:"channel"`
		ReleaseNotes string         `json:"releaseNotes"`
	}
	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		h.write(w, nil, err, http.StatusBadRequest)
		return
	}
	body.Channel = ReleaseChannel(strings.ToUpper(string(body.Channel)))
	v, err := h.srv.ConfirmUpload(projectID, body.Token, body.Channel, body.ReleaseNotes, publicURL(r))
	h.write(w, map[string]interface{}{"release": v}, err, http.StatusCreated)
}

func (h *APIHandler) upload(w http.ResponseWriter, r *http.Request, projectID string) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.write(w, nil, err, http.StatusBadRequest)
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		h.write(w, nil, err, http.StatusBadRequest)
		return
	}
	defer f.Close()
	t := FileType(header.Filename)
	if t == AppInfoTypeUnknown {
		h.write(w, nil, fmt.Errorf("do not support %s file", path.Ext(header.Filename)), http.StatusBadRequest)
		return
	}
	buf, err := seekbuf.Open(f, seekbuf.FileMode)
	if err != nil {
		h.write(w, nil, err, http.StatusBadRequest)
		return
	}
	defer buf.Close()
	channel := ReleaseChannel(strings.ToUpper(common.Def(r.FormValue("channel"), string(ChannelTest))))
	v, err := h.srv.UploadRelease(projectID, buf, header.Size, t, channel, r.FormValue("releaseNotes"), publicURL(r))
	h.write(w, map[string]interface{}{"release": v}, err, http.StatusCreated)
}

func (h *APIHandler) release(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/releases/"))
	if len(parts) == 0 {
		h.write(w, nil, errors.New("not found"), http.StatusNotFound)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			v, err := h.srv.GetRelease(id, publicURL(r))
			h.write(w, map[string]interface{}{"release": v}, err, http.StatusOK)
		case http.MethodDelete:
			if !h.deleteEnabled {
				h.write(w, nil, errors.New("no permission to delete"), http.StatusForbidden)
				return
			}
			h.write(w, map[string]string{"msg": "ok"}, h.srv.DeleteRelease(id), http.StatusOK)
		default:
			h.write(w, nil, errors.New("method not allowed"), http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "notes" && r.Method == http.MethodPut {
		var body struct {
			ReleaseNotes string `json:"releaseNotes"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil {
			h.write(w, nil, err, http.StatusBadRequest)
			return
		}
		v, err := h.srv.UpdateReleaseNotes(id, body.ReleaseNotes, publicURL(r))
		h.write(w, map[string]interface{}{"release": v}, err, http.StatusOK)
		return
	}
	if len(parts) == 2 && parts[1] == "promote" && r.Method == http.MethodPost {
		v, err := h.srv.PromoteRelease(id, publicURL(r))
		h.write(w, map[string]interface{}{"release": v}, err, http.StatusOK)
		return
	}
	h.write(w, nil, errors.New("not found"), http.StatusNotFound)
}

func (h *APIHandler) write(w http.ResponseWriter, value interface{}, err error, successStatus int) {
	if err != nil {
		status := successStatus
		if status < 400 {
			status = http.StatusInternalServerError
		}
		switch {
		case errors.Is(err, ErrIDNotFound):
			status = http.StatusNotFound
		case errors.Is(err, ErrProjectNameRequired), errors.Is(err, ErrInvalidChannel):
			status = http.StatusBadRequest
		case errors.Is(err, ErrIdentifierConflict):
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(successStatus)
	_ = json.NewEncoder(w).Encode(value)
}

func NewPlistHandler(srv Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(path.Base(r.URL.Path), ".plist")
		data, err := srv.Plist(id, publicURL(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write(data)
	})
}

func splitPath(v string) []string {
	raw := strings.Split(strings.Trim(v, "/"), "/")
	result := make([]string, 0, len(raw))
	for _, part := range raw {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func publicURL(r *http.Request) string {
	if ref := r.Header.Get("referer"); ref != "" {
		u, _ := url.Parse(ref)
		return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	}
	return fmt.Sprintf("%s://%s", common.Def(r.Header.Get("x-forwarded-proto"), "http"), r.Host)
}
