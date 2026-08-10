package main

import (
	"crypto/subtle"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/billcoding/proctor/internal/config"
	"github.com/billcoding/proctor/internal/server"
	"github.com/billcoding/proctor/web"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	configPath := flag.String("config", "./configs/server.json", "server config path")
	listen := flag.String("listen", "", "override listen address")
	flag.Parse()

	cfg, err := config.LoadServer(*configPath)
	if err != nil {
		if _, statErr := os.Stat(*configPath); os.IsNotExist(statErr) {
			cfg = config.DefaultServer()
		} else {
			log.Fatalf("load config: %v", err)
		}
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "updates"), 0o755); err != nil {
		log.Fatal(err)
	}

	store, err := server.OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	api := server.NewAPI(store, cfg)
	mux := http.NewServeMux()
	api.Routes(mux)

	staticHandler, err := staticFS(cfg.StaticDir)
	if err != nil {
		log.Fatalf("static: %v", err)
	}
	mux.Handle("/", staticHandler)

	handler := withCORS(withBasicAuth(cfg, mux))
	if cfg.BasicAuthPassword != "" {
		log.Printf("proctor-server listening on %s (basic auth enabled, admin token configured)", cfg.Listen)
	} else {
		log.Printf("proctor-server listening on %s (basic auth disabled, admin token configured)", cfg.Listen)
	}
	if err := http.ListenAndServe(cfg.Listen, handler); err != nil {
		log.Fatal(err)
	}
}

func staticFS(dir string) (http.Handler, error) {
	if dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return http.FileServer(http.Dir(dir)), nil
		}
		if exe, err := os.Executable(); err == nil {
			cand := filepath.Join(filepath.Dir(exe), "web", "static")
			if st, err := os.Stat(cand); err == nil && st.IsDir() {
				return http.FileServer(http.Dir(cand)), nil
			}
		}
	}
	sub, err := fs.Sub(web.Static, "static")
	if err != nil {
		return nil, err
	}
	return http.FileServer(http.FS(sub)), nil
}

// withBasicAuth protects the console and management APIs.
// Agent paths (/api/agent/*) are skipped so agents keep using agent_token.
// Empty basic_auth_password disables this gate.
func withBasicAuth(cfg config.ServerConfig, next http.Handler) http.Handler {
	if cfg.BasicAuthPassword == "" {
		return next
	}
	user := cfg.BasicAuthUser
	if user == "" {
		user = "proctor"
	}
	wantUser := []byte(user)
	wantPass := []byte(cfg.BasicAuthPassword)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/agent/") {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), wantUser) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), wantPass) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Proctor"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Admin-Token, X-Agent-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
