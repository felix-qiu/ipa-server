package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/iineva/ipa-server/cmd/ipasd/service"
	"github.com/iineva/ipa-server/pkg/http_basic_auth"
	"github.com/iineva/ipa-server/pkg/httpfs"
	"github.com/iineva/ipa-server/pkg/storager"
	"github.com/iineva/ipa-server/pkg/uuid"
	"github.com/iineva/ipa-server/public"
	"github.com/spf13/afero"
)

func redirect(m map[string]string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target, ok := m[r.URL.Path]; ok {
			r.URL.Path = target
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", "0.0.0.0", "bind addr")
	port := flag.String("port", "8080", "bind port")
	debug := flag.Bool("d", false, "enable debug logging")
	user := flag.String("user", "", "basic auth username")
	pass := flag.String("pass", "", "basic auth password")
	storageDir := flag.String("dir", "upload", "upload data storage dir")
	publicURL := flag.String("public-url", "", "server public url")
	metadataPath := flag.String("meta-path", "appList.json", "legacy metadata path used for one-time migration")
	databasePath := flag.String("db-path", "", "sqlite database path, defaults to <dir>/ipa-server.db")
	deleteEnabled := flag.Bool("del", false, "delete release enabled")
	uploadDisabled := flag.Bool("upload-disabled", false, "disable release upload")
	remoteCfg := flag.String("remote", "", "remote storager config, s3://ENDPOINT:AK:SK:BUCKET, alioss://ENDPOINT:AK:SK:BUCKET, qiniu://[ZONE]:AK:SK:BUCKET")
	remoteURL := flag.String("remote-url", "", "remote storager public url, https://cdn.example.com")
	realm := "App Distribution"
	flag.Usage = usage
	flag.Parse()

	logger := log.NewLogfmtLogger(os.Stderr)
	logger = log.With(logger, "ts", log.TimestampFormat(time.Now, "2006-01-02 15:04:05.000"), "caller", log.DefaultCaller)
	_ = debug // retained for CLI compatibility; API logs are emitted by the HTTP server.

	var store storager.Storager
	if *remoteCfg != "" && *remoteURL != "" {
		r := strings.Split(*remoteCfg, "://")
		if len(r) != 2 {
			usage()
			os.Exit(0)
		}
		args := strings.Split(r[1], ":")
		if len(args) != 4 {
			usage()
			os.Exit(0)
		}
		switch r[0] {
		case "s3":
			store = mustStore(storager.NewS3Storager(args[0], args[1], args[2], args[3], *remoteURL))
		case "alioss":
			store = mustStore(storager.NewAliOssStorager(args[0], args[1], args[2], args[3], *remoteURL))
		case "qiniu":
			store = mustStore(storager.NewQiniuStorager(args[0], args[1], args[2], args[3], *remoteURL))
		default:
			panic("unsupported remote storager")
		}
	} else {
		store = storager.NewOsFileStorager(*storageDir)
	}

	if *databasePath == "" {
		*databasePath = filepath.Join(*storageDir, "ipa-server.db")
	}
	srv, err := service.New(store, *publicURL, *databasePath, *metadataPath)
	if err != nil {
		panic(err)
	}
	defer srv.Close()

	serve := http.NewServeMux()
	api := service.NewAPIHandler(srv, !*uploadDisabled, *deleteEnabled)
	serve.Handle("/api/", authMutations(*user, *pass, realm, api))
	serve.Handle("/plist/", service.NewPlistHandler(srv))

	uploadFS := afero.NewBasePathFs(afero.NewOsFs(), *storageDir)
	staticFS := httpfs.New(http.FS(public.FS), httpfs.NewAferoFS(uploadFS))
	hidden := map[string]string{fmt.Sprintf("/%s", *metadataPath): "/" + uuid.NewString()}
	if rel, err := filepath.Rel(*storageDir, *databasePath); err == nil && !strings.HasPrefix(rel, "..") {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			hidden["/"+filepath.ToSlash(rel)+suffix] = "/" + uuid.NewString()
		}
	}
	serve.Handle("/", redirect(hidden, http.FileServer(staticFS)))

	host := fmt.Sprintf("%s:%s", *addr, *port)
	_ = logger.Log("msg", fmt.Sprintf("SERVER LISTEN ON: http://%s", host))
	_ = logger.Log("msg", http.ListenAndServe(host, serve))
}

func mustStore(s storager.Storager, err error) storager.Storager {
	if err != nil {
		panic(err)
	}
	return s
}

func authMutations(user, pass, realm string, next http.Handler) http.Handler {
	if user == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if err := http_basic_auth.HandleBasicAuth(user, pass, realm, r); err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: ipa-server [options]")
	flag.PrintDefaults()
}
